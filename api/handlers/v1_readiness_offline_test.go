package handlers

import (
	"context"
	"strings"
	"testing"

	"core/types"
)

func TestReadinessReportsOfflineModeAsNotReady(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("GATEWAY_OFFLINE", "true")

	checks := v1RunReadinessChecks(context.Background(), V1APIDeps{})
	offline := findReadinessCheck(checks, "offline.mode")
	if offline == nil {
		t.Fatalf("readiness checks %v do not contain the offline.mode marker", readinessCheckNames(checks))
	}
	if offline.OK {
		t.Fatal("offline.mode must not report ok=true: offline mode is never production ready")
	}
	for _, token := range []string{"GATEWAY_OFFLINE=true", "not production ready"} {
		if !strings.Contains(offline.Details, token) {
			t.Fatalf("offline.mode details %q must mention %q", offline.Details, token)
		}
	}
	if strings.TrimSpace(offline.Error) == "" {
		t.Fatal("offline.mode must carry an error so aggregate readiness stays false")
	}
	if v1ReadinessOK(checks) {
		t.Fatal("aggregate readiness must not be reported as ready while offline mode is active")
	}
}

func TestReadinessOmitsOfflineMarkerByDefault(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("GATEWAY_OFFLINE", "")

	checks := v1RunReadinessChecks(context.Background(), V1APIDeps{})
	if marker := findReadinessCheck(checks, "offline.mode"); marker != nil {
		t.Fatalf("offline.mode must only appear when offline mode is enabled, got %+v", *marker)
	}
}

func TestReadinessOfflineMarkerAbsentWhenPolicyFailsClosed(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("GATEWAY_OFFLINE", "true")

	checks := v1RunReadinessChecks(context.Background(), V1APIDeps{})
	if marker := findReadinessCheck(checks, "offline.mode"); marker != nil {
		t.Fatalf("offline mode must not be reported as active when the policy fails closed, got %+v", *marker)
	}
}

func findReadinessCheck(checks []types.V1ReadinessCheck, name string) *types.V1ReadinessCheck {
	for index := range checks {
		if checks[index].Name == name {
			return &checks[index]
		}
	}
	return nil
}

func readinessCheckNames(checks []types.V1ReadinessCheck) []string {
	names := make([]string, 0, len(checks))
	for _, check := range checks {
		names = append(names, check.Name)
	}
	return names
}
