package helpers

import (
	"net"
	"testing"
)

// A9: the webhook destination IP policy is shared by URL-time validation and the
// transport's per-connection validation, so it is tested directly here.

func TestWebhookDestinationPolicyRejectsNonPublicInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	// The non-production exception flag must never widen production egress.
	t.Setenv("ALLOW_PRIVATE_WEBHOOK_URLS", "true")

	blocked := []string{
		"10.0.0.1", "172.16.5.4", "192.168.1.1", // private IPv4
		"127.0.0.1", "::1", // loopback
		"169.254.169.254", "fe80::1", // link-local (incl. the metadata address)
		"100.64.0.1",              // carrier-grade NAT
		"fd12:3456::1", "fc00::1", // IPv6 unique local
		"::ffff:10.0.0.1",      // IPv4-mapped private
		"224.0.0.1", "ff02::1", // multicast
		"0.0.0.0", "255.255.255.255", // unspecified / broadcast
		"240.0.0.1",              // reserved 240.0.0.0/4
		"192.0.0.9",              // IETF protocol assignments
		"198.18.0.1",             // benchmarking
		"2002:0a00:0001::1",      // 6to4 tunnelling a private IPv4 target
		"2001:0000:4136:e378::1", // Teredo
	}
	for _, raw := range blocked {
		if WebhookDestinationAllowed(net.ParseIP(raw)) {
			t.Fatalf("production allowed a non-public webhook destination %s", raw)
		}
	}

	allowed := []string{"93.184.216.34", "1.1.1.1", "2606:4700:4700::1111"}
	for _, raw := range allowed {
		if !WebhookDestinationAllowed(net.ParseIP(raw)) {
			t.Fatalf("production refused the public webhook destination %s", raw)
		}
	}
	if WebhookDestinationAllowed(nil) {
		t.Fatal("a nil IP must never be dialled")
	}
}

func TestWebhookDestinationPolicyAllowsLocalReceiversOnlyOutsideProduction(t *testing.T) {
	t.Setenv("ALLOW_PRIVATE_WEBHOOK_URLS", "")
	t.Setenv("APP_ENV", "development")
	for _, raw := range []string{"127.0.0.1", "::1", "192.168.1.20", "100.64.0.1", "fd12:3456::1"} {
		if !WebhookDestinationAllowed(net.ParseIP(raw)) {
			t.Fatalf("development must keep the local receiver exception for %s", raw)
		}
	}
	// Destinations that are never valid stay blocked even in development.
	for _, raw := range []string{"0.0.0.0", "255.255.255.255", "224.0.0.1", "ff02::1", "240.0.0.1", "198.18.0.1", "2002:0a00:0001::1"} {
		if WebhookDestinationAllowed(net.ParseIP(raw)) {
			t.Fatalf("development allowed a never-valid destination %s", raw)
		}
	}

	t.Setenv("APP_ENV", "production")
	if WebhookDestinationAllowed(net.ParseIP("127.0.0.1")) {
		t.Fatal("production must never allow a loopback webhook receiver")
	}
	if !WebhookDestinationAllowed(net.ParseIP("93.184.216.34")) {
		t.Fatal("production must still allow a public webhook receiver")
	}
}

func TestValidateWebhookURLRejectsInlineCredentialsZonesAndBadPorts(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	for _, raw := range []string{
		"https://user:pass@example.com/hook",
		"https://example.com:99999/hook",
		"https://example.com:notaport/hook",
		"https://[fe80::1%25eth0]/hook",
	} {
		if err := ValidateWebhookURL(raw); err == nil {
			t.Fatalf("ValidateWebhookURL(%q) = nil, want a rejection", raw)
		}
	}
}
