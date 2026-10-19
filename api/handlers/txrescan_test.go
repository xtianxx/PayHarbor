package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"core/constants"
	"core/localization"
	"core/services/txrescan"
)

func TestParseTxRescanChainAcceptsIDsAndSlugs(t *testing.T) {
	tests := map[string]constants.ChainID{
		"1":            constants.Ethereum,
		"ethereum":     constants.Ethereum,
		"ETHEREUM":     constants.Ethereum,
		"base":         constants.Base,
		"bnbchain":     constants.Binance,
		"bsc":          constants.Binance,
		"bitcoin":      constants.Bitcoin,
		"solana":       constants.Solana,
		"tron":         constants.TRON,
		"tron-testnet": constants.TRONTestnet,
		"nile":         constants.TRONTestnet,
		"tron-nile":    constants.TRONTestnet,
		"shasta":       constants.TRONTestnet,
		"chiliz-spicy": constants.ChilizSpicy,
		"arbitrum-one": constants.Arbitrum,
		"99999998":     constants.TRON,
		"99999997":     constants.TRONTestnet,
		"99999999":     constants.Solana,
	}
	for input, expected := range tests {
		got, err := parseTxRescanChain(input)
		if err != nil {
			t.Fatalf("parseTxRescanChain(%q) error: %v", input, err)
		}
		if got != expected {
			t.Fatalf("parseTxRescanChain(%q) = %d, want %d", input, got, expected)
		}
	}
}

func TestParseTxRescanChainRejectsUnsupportedIDs(t *testing.T) {
	for _, input := range []string{"", "554576", "unknown-chain"} {
		if _, err := parseTxRescanChain(input); err == nil {
			t.Fatalf("parseTxRescanChain(%q) should fail", input)
		}
	}
}

func TestTxRescanMessages(t *testing.T) {
	if txRescanRequestTimeout != 90*time.Second {
		t.Fatalf("txRescanRequestTimeout = %s, want 90s", txRescanRequestTimeout)
	}
	if got := txRescanSuccessMessage(localization.EN, &txrescan.Result{Chain: "ethereum", Events: 2}); got != "ethereum transaction rescanned: 2 events processed." {
		t.Fatalf("success message = %q", got)
	}
	got := txRescanSuccessMessage(localization.EN, &txrescan.Result{
		Chain:                "tron",
		Events:               1,
		DepositsMatched:      1,
		TransactionsRecorded: 1,
		DepositsFinalized:    1,
	})
	want := "tron transaction rescanned: 1 events processed, 1 deposits matched, 1 transactions recorded, 1 deposits finalized."
	if got != want {
		t.Fatalf("detailed success message = %q, want %q", got, want)
	}
	got = txRescanSuccessMessage(localization.EN, &txrescan.Result{Chain: "chiliz", Events: 1, DepositsUnmatched: 1})
	want = "chiliz transaction rescanned: 1 events processed, 1 transactions did not match a wallet; no deposits written."
	if got != want {
		t.Fatalf("unmatched success message = %q, want %q", got, want)
	}
	tests := map[error]string{
		txrescan.ErrTransactionNotFound: "Transaction was not found on the blockchain.",
		txrescan.ErrUnauthorizedTx:      "This transaction does not match the merchant wallet addresses.",
		txrescan.ErrUnsupportedChain:    "Rescan is not supported for this network.",
		context.DeadlineExceeded:        "Rescan timed out. The request was sent, but the blockchain RPC response timed out; completion could not be verified. Try again later.",
		context.Canceled:                "Rescan was canceled before completion. The transaction could not be verified as complete.",
		errors.New("custom error"):      "custom error",
	}
	for err, expected := range tests {
		if got := txRescanErrorMessage(localization.EN, err); got != expected {
			t.Fatalf("error message for %v = %q, want %q", err, got, expected)
		}
	}
	wrappedTimeout := errors.New("chiliz https://rpc.chiliz.com request failed: timeout: context deadline exceeded")
	want = "Rescan timed out. The request was sent, but the blockchain RPC response timed out; completion could not be verified. Try again later."
	if got := txRescanErrorMessage(localization.EN, wrappedTimeout); got != want {
		t.Fatalf("wrapped timeout message = %q, want %q", got, want)
	}
}

func TestFirstNonEmptyTrimsForDecisionButReturnsOriginal(t *testing.T) {
	if got := firstNonEmpty("", "   ", " tx "); got != " tx " {
		t.Fatalf("firstNonEmpty = %q", got)
	}
	if got := firstNonEmpty("", " "); got != "" {
		t.Fatalf("empty firstNonEmpty = %q", got)
	}
}
