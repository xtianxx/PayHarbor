package webhook

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"core/helpers"
)

// Webhook egress client (A9).
//
// A webhook URL is validated when it is stored, but validation and connection must
// not be separable: a hostname can resolve to a public address during validation
// and to a private one when the request is actually dialled. The default
// http.Client additionally follows redirects (replaying the signed payload to
// whatever the target points at) and honours environment HTTP proxies.
//
// The client built here therefore:
//   - never follows redirects: a 3xx response is returned to the caller and handled
//     as a delivery failure by the existing non-2xx path, so the payload and its
//     HMAC header are never sent to a redirect target;
//   - resolves the destination host for every new connection and validates ALL
//     candidate addresses with the shared helpers.WebhookDestinationAllowed policy
//     before dialling (fail closed on empty results or resolution errors);
//   - dials the verified address literal with the original port, so the request
//     Host header and TLS SNI stay the original hostname and normal certificate
//     verification still applies (no InsecureSkipVerify);
//   - ignores HTTP_PROXY/HTTPS_PROXY/ALL_PROXY (Proxy: nil);
//   - keeps the 15s client timeout, context cancellation and connection reuse, and
//     adds no retry loop of its own.

const webhookClientTimeout = 15 * time.Second

type webhookResolveFunc func(ctx context.Context, host string) ([]net.IP, error)

type webhookDialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// safeWebhookClientOptions carries test-only injections used by package tests.
// Production always calls defaultSafeWebhookHTTPClient, which leaves every field
// empty, so no production call site can disable the redirect or destination policy.
type safeWebhookClientOptions struct {
	resolveIPs  webhookResolveFunc
	dialContext webhookDialFunc
	tlsConfig   *tls.Config
	timeout     time.Duration
}

func defaultSafeWebhookHTTPClient() *http.Client {
	return newSafeWebhookHTTPClient(safeWebhookClientOptions{})
}

func newSafeWebhookHTTPClient(opts safeWebhookClientOptions) *http.Client {
	dialer := &safeWebhookDialer{
		resolveIPs:  opts.resolveIPs,
		dialContext: opts.dialContext,
	}
	if dialer.resolveIPs == nil {
		dialer.resolveIPs = resolveWebhookHostIPs
	}
	if dialer.dialContext == nil {
		dialer.dialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	timeout := opts.timeout
	if timeout <= 0 {
		timeout = webhookClientTimeout
	}
	transport := &http.Transport{
		// Never inherit HTTP_PROXY/HTTPS_PROXY/ALL_PROXY for webhook egress.
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSClientConfig:       opts.tlsConfig,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			// Hand the 3xx response back instead of replaying the signed payload at
			// the redirect target.
			return http.ErrUseLastResponse
		},
	}
}

func resolveWebhookHostIPs(ctx context.Context, host string) ([]net.IP, error) {
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		ips = append(ips, address.IP)
	}
	return ips, nil
}

type safeWebhookDialer struct {
	resolveIPs  webhookResolveFunc
	dialContext webhookDialFunc
}

func (d *safeWebhookDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("webhook dial: invalid address %q: %w", address, err)
	}
	host = strings.TrimSpace(host)
	if host == "" || strings.TrimSpace(port) == "" {
		return nil, errors.New("webhook dial: host and port are required")
	}
	if value, portErr := strconv.Atoi(port); portErr != nil || value < 1 || value > 65535 {
		return nil, fmt.Errorf("webhook dial: invalid port %q", port)
	}
	var candidates []net.IP
	if literal := net.ParseIP(host); literal != nil {
		candidates = []net.IP{literal}
	} else {
		candidates, err = d.resolveIPs(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("webhook dial: resolve %q failed: %w", host, err)
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("webhook dial: %q resolved to no addresses", host)
	}
	for _, ip := range candidates {
		if !helpers.WebhookDestinationAllowed(ip) {
			return nil, fmt.Errorf("webhook dial: refusing non-public webhook destination %s", ip.String())
		}
	}
	// Dial only the verified address literals: the request host (and therefore the
	// Host header and TLS SNI) remains the original hostname.
	var lastErr error
	for _, ip := range candidates {
		conn, dialErr := d.dialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
		if ctx.Err() != nil {
			break
		}
	}
	return nil, lastErr
}
