package helpers

import (
	"fmt"
	"os"
	"strings"
)

// OfflineModeEnvKey is the explicit, development-only switch that runs the
// gateway without chain listeners and without periodic money/event workers.
const OfflineModeEnvKey = "GATEWAY_OFFLINE"

// offlineModeTruthy mirrors the truthy values accepted by the gateway's other
// boolean environment flags, so "GATEWAY_OFFLINE=on" behaves like any other
// flag. Anything else (including unset, "0", "false", or garbage) is falsey and
// therefore keeps the default runtime behavior.
var offlineModeTruthy = map[string]struct{}{
	"1":    {},
	"true": {},
	"yes":  {},
	"on":   {},
}

// OfflineModeRequested reports whether the operator explicitly asked for
// offline mode. It performs no policy validation; callers that need the
// validated answer must use OfflineModeEnabled or OfflineModePolicyError.
func OfflineModeRequested() bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(OfflineModeEnvKey)))
	_, ok := offlineModeTruthy[raw]
	return ok
}

// appEnvIsDevelopment reports whether APP_ENV unambiguously selects
// development. Unset, empty, or any other value is not development.
func appEnvIsDevelopment() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "development")
}

// OfflineModePolicyError returns a non-nil error when offline mode is requested
// in a configuration where it must fail closed. Offline mode is only valid for
// an explicit APP_ENV=development process; in production, staging, or any
// unset/ambiguous environment the gateway refuses to start instead of silently
// running without chain listeners. It never weakens the production signer,
// readiness, or Trust Wallet Core native-build requirements.
func OfflineModePolicyError() error {
	if !OfflineModeRequested() {
		return nil
	}
	if !appEnvIsDevelopment() {
		return fmt.Errorf(
			"%s=true is only allowed with APP_ENV=development (got %q); offline mode fails closed in every other environment",
			OfflineModeEnvKey,
			strings.TrimSpace(os.Getenv("APP_ENV")),
		)
	}
	return nil
}

// OfflineModeEnabled reports whether this process must skip chain listeners,
// address-index preload, and every periodic money/event worker. It is the single
// source of truth for the offline runtime contract and is safe to call from
// startup code and HTTP handlers.
func OfflineModeEnabled() bool {
	return OfflineModeRequested() && OfflineModePolicyError() == nil
}
