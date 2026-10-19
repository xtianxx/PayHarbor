package webhook

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"core/constants"
	"core/helpers"
	"core/models"

	"github.com/google/uuid"
)

// A9: webhook egress must not follow redirects, must ignore environment proxies,
// must validate every connection destination, and must keep the original Host/SNI
// while dialling a verified address literal. All tests stay local (httptest) and
// never touch the public network.

func webhookTestDomain(t *testing.T, webhookURL string) models.Domain {
	t.Helper()
	t.Setenv("MASTER_KEY", "webhook-test-master-key")
	secret, err := helpers.EncryptSecret("plain-webhook-secret")
	if err != nil {
		t.Fatalf("encrypt webhook secret: %v", err)
	}
	return models.Domain{ID: uuid.New(), WebhookURL: webhookURL, WebhookSecret: secret}
}

func webhookTestTransaction() models.Transaction {
	merchantID := uuid.New()
	domainID := uuid.New()
	walletID := uuid.New()
	return models.Transaction{
		ID:          uuid.New(),
		UniqueHash:  "1-0xabc-a9:" + uuid.NewString(),
		EventType:   "native_transfer",
		MerchantID:  &merchantID,
		DomainID:    &domainID,
		WalletID:    &walletID,
		ProductID:   "product",
		UserID:      "user",
		ChainID:     constants.Ethereum,
		Hash:        "0xabc",
		BlockNumber: "123",
		BlockHash:   "0xblock",
		Symbol:      "ETH",
		Decimals:    18,
		FromAddress: "0xfrom",
		ToAddress:   "0xto",
		Amount:      "100",
		Status:      models.TransactionStatusConfirmed,
		CreatedAt:   time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC),
	}
}

func TestNewNotifierUsesTheSafeEgressClient(t *testing.T) {
	notifier := NewNotifier()
	if notifier.client == nil {
		t.Fatal("NewNotifier must configure an HTTP client")
	}
	if notifier.client.Timeout != 15*time.Second {
		t.Fatalf("client timeout = %s, want the existing 15s", notifier.client.Timeout)
	}
	if notifier.client.CheckRedirect == nil {
		t.Fatal("the production webhook client must never follow redirects")
	}
	if err := notifier.client.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirect policy = %v, want http.ErrUseLastResponse", err)
	}
	transport, ok := notifier.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", notifier.client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("the webhook transport must not use environment HTTP proxies")
	}
	if transport.DialContext == nil {
		t.Fatal("the webhook transport must validate destinations when it dials")
	}
	if transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("the webhook transport must keep normal certificate verification")
	}
}

func TestWebhookDeliveryNeverFollowsRedirects(t *testing.T) {
	t.Setenv("APP_ENV", "test")

	var redirectTargetHits int64
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&redirectTargetHits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer redirectTarget.Close()

	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		var firstHits int64
		var signature, timestamp, body string
		first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt64(&firstHits, 1)
			signature = r.Header.Get("X-Gateway-Signature")
			timestamp = r.Header.Get("X-Gateway-Timestamp")
			raw, _ := io.ReadAll(r.Body)
			body = string(raw)
			w.Header().Set("Location", redirectTarget.URL+"/steal")
			w.WriteHeader(status)
		}))

		domain := webhookTestDomain(t, first.URL+"/hook")
		err := NewNotifier().Deliver(context.Background(), domain, webhookTestTransaction())
		first.Close()

		if err == nil {
			t.Fatalf("status %d: a redirect must be reported as a delivery failure", status)
		}
		if !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", status)) {
			t.Fatalf("status %d: error = %v, want the non-2xx failure", status, err)
		}
		if hits := atomic.LoadInt64(&firstHits); hits != 1 {
			t.Fatalf("status %d: original receiver hits = %d, want exactly 1", status, hits)
		}
		if hits := atomic.LoadInt64(&redirectTargetHits); hits != 0 {
			t.Fatalf("status %d: the payload was sent to the redirect target (%d hits)", status, hits)
		}
		if !strings.HasPrefix(signature, "sha256=") || !helpers.VerifySignature("plain-webhook-secret", timestamp, []byte(body), strings.TrimPrefix(signature, "sha256=")) {
			t.Fatalf("status %d: the original receiver must get the signed payload", status)
		}
	}
}

func TestWebhookDeliveryIgnoresEnvironmentProxies(t *testing.T) {
	t.Setenv("APP_ENV", "test")

	var proxyHits int64
	decoyProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&proxyHits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer decoyProxy.Close()
	t.Setenv("HTTP_PROXY", decoyProxy.URL)
	t.Setenv("HTTPS_PROXY", decoyProxy.URL)
	t.Setenv("ALL_PROXY", decoyProxy.URL)
	t.Setenv("NO_PROXY", "")

	var targetHits int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&targetHits, 1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer target.Close()

	domain := webhookTestDomain(t, target.URL+"/hook")
	if err := NewNotifier().Deliver(context.Background(), domain, webhookTestTransaction()); err != nil {
		t.Fatalf("delivery with decoy proxies configured: %v", err)
	}
	if hits := atomic.LoadInt64(&targetHits); hits != 1 {
		t.Fatalf("target hits = %d, want 1", hits)
	}
	if hits := atomic.LoadInt64(&proxyHits); hits != 0 {
		t.Fatalf("the webhook used the environment proxy (%d hits)", hits)
	}
}

func TestWebhookTransportDialsVerifiedLiteralAndKeepsHostAndSNI(t *testing.T) {
	t.Setenv("APP_ENV", "test")

	var receivedHost, receivedSNI string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHost = r.Host
		if r.TLS != nil {
			receivedSNI = r.TLS.ServerName
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	port := strings.TrimPrefix(server.URL, "https://127.0.0.1:")
	if port == server.URL {
		t.Fatalf("unexpected test server url %q", server.URL)
	}

	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())

	var resolveCalls int64
	var dialMu sync.Mutex
	var dialed []string
	client := newSafeWebhookHTTPClient(safeWebhookClientOptions{
		// Trust the local test certificate; verification itself stays enabled.
		tlsConfig: &tls.Config{RootCAs: pool},
		resolveIPs: func(context.Context, string) ([]net.IP, error) {
			atomic.AddInt64(&resolveCalls, 1)
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		},
		dialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialMu.Lock()
			dialed = append(dialed, address)
			dialMu.Unlock()
			return (&net.Dialer{}).DialContext(ctx, network, address)
		},
	})

	// "example.com" matches the httptest certificate SAN; the resolver maps it to
	// the local listener, so this exercises the hostname/Host/SNI path offline.
	request, err := http.NewRequest(http.MethodPost, "https://example.com:"+port+"/hook", strings.NewReader(`{"ping":true}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("delivery: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", response.StatusCode)
	}
	if receivedHost != "example.com:"+port {
		t.Fatalf("Host header = %q, want the original hostname and port", receivedHost)
	}
	if receivedSNI != "example.com" {
		t.Fatalf("TLS SNI = %q, want the original hostname", receivedSNI)
	}
	if calls := atomic.LoadInt64(&resolveCalls); calls != 1 {
		t.Fatalf("resolve calls = %d, want exactly 1 (the transport must resolve once and dial the literal)", calls)
	}
	dialMu.Lock()
	defer dialMu.Unlock()
	if len(dialed) != 1 {
		t.Fatalf("dialed addresses = %v, want exactly one", dialed)
	}
	host, dialPort, err := net.SplitHostPort(dialed[0])
	if err != nil {
		t.Fatalf("dial address %q: %v", dialed[0], err)
	}
	if net.ParseIP(host) == nil {
		t.Fatalf("dialer received %q instead of a verified address literal", dialed[0])
	}
	if dialPort != port {
		t.Fatalf("dial port = %q, want the original port %q", dialPort, port)
	}
}

func TestWebhookDialerValidatesEveryConnectionAddress(t *testing.T) {
	cases := []struct {
		name       string
		env        string
		allowFlag  bool
		ips        []string
		resolveErr error
		wantRefuse bool
	}{
		{name: "public candidate", env: "production", ips: []string{"93.184.216.34"}},
		{name: "validation public then dial private", env: "production", ips: []string{"93.184.216.34", "10.0.0.5"}, wantRefuse: true},
		{name: "mixed public and private", env: "production", ips: []string{"10.0.0.5", "93.184.216.34"}, wantRefuse: true},
		{name: "ipv4-mapped private", env: "production", ips: []string{"::ffff:10.0.0.1"}, wantRefuse: true},
		{name: "loopback", env: "production", ips: []string{"127.0.0.1"}, wantRefuse: true},
		{name: "link-local metadata", env: "production", ips: []string{"169.254.169.254"}, wantRefuse: true},
		{name: "carrier-grade nat", env: "production", ips: []string{"100.64.0.1"}, wantRefuse: true},
		{name: "ipv6 unique local", env: "production", ips: []string{"fd12:3456::1"}, wantRefuse: true},
		{name: "multicast", env: "production", ips: []string{"224.0.0.1"}, wantRefuse: true},
		{name: "empty resolution", env: "production", wantRefuse: true},
		{name: "resolution error", env: "production", resolveErr: errors.New("dns unavailable"), wantRefuse: true},
		{name: "production ignores the private flag", env: "production", allowFlag: true, ips: []string{"10.0.0.1"}, wantRefuse: true},
		{name: "development keeps loopback receivers", env: "development", ips: []string{"127.0.0.1"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("APP_ENV", testCase.env)
			if testCase.allowFlag {
				t.Setenv("ALLOW_PRIVATE_WEBHOOK_URLS", "true")
			}
			var dialed []string
			dialer := &safeWebhookDialer{
				resolveIPs: func(context.Context, string) ([]net.IP, error) {
					if testCase.resolveErr != nil {
						return nil, testCase.resolveErr
					}
					ips := make([]net.IP, 0, len(testCase.ips))
					for _, raw := range testCase.ips {
						ips = append(ips, net.ParseIP(raw))
					}
					return ips, nil
				},
				dialContext: func(_ context.Context, _, address string) (net.Conn, error) {
					dialed = append(dialed, address)
					return nil, errors.New("stub dialer refuses every connection")
				},
			}

			conn, err := dialer.DialContext(context.Background(), "tcp", "rebind.example.com:8443")
			if conn != nil {
				t.Fatal("the stub dialer never returns a connection")
			}
			if testCase.wantRefuse {
				if err == nil {
					t.Fatal("dial must fail closed")
				}
				if len(dialed) != 0 {
					t.Fatalf("the underlying dialer was called for a refused destination: %v", dialed)
				}
				return
			}
			if err == nil {
				t.Fatal("the stub dialer error must surface")
			}
			if len(dialed) != 1 || dialed[0] != net.JoinHostPort(testCase.ips[0], "8443") {
				t.Fatalf("dialed = %v, want the verified literal with the original port", dialed)
			}
		})
	}
}

func TestWebhookDeliveryFailsOnCancelledContextAndTimeout(t *testing.T) {
	t.Setenv("APP_ENV", "test")

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer slow.Close()
	domain := webhookTestDomain(t, slow.URL+"/hook")

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NewNotifier().Deliver(cancelled, domain, webhookTestTransaction()); err == nil {
		t.Fatal("a cancelled context must not be reported as a successful delivery")
	}

	timeoutNotifier := &Notifier{client: newSafeWebhookHTTPClient(safeWebhookClientOptions{timeout: 50 * time.Millisecond})}
	if err := timeoutNotifier.Deliver(context.Background(), domain, webhookTestTransaction()); err == nil {
		t.Fatal("a client timeout must not be reported as a successful delivery")
	}
	// A slow but successful caller context still works with the default timeout.
	if err := NewNotifier().Deliver(context.Background(), domain, webhookTestTransaction()); err != nil {
		t.Fatalf("delivery to a slow receiver within the timeout: %v", err)
	}
}
