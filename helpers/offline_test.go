package helpers

import (
	"strings"
	"testing"
)

func TestOfflineModePolicyMatrix(t *testing.T) {
	tests := []struct {
		name          string
		appEnv        string
		offlineValue  string
		wantRequested bool
		wantEnabled   bool
		wantPolicyErr bool
	}{
		{
			name:          "explicit development enables offline mode",
			appEnv:        "development",
			offlineValue:  "true",
			wantRequested: true,
			wantEnabled:   true,
		},
		{
			name:          "development accepts numeric truthy value",
			appEnv:        "development",
			offlineValue:  "1",
			wantRequested: true,
			wantEnabled:   true,
		},
		{
			name:          "development accepts case and whitespace variants",
			appEnv:        "Development",
			offlineValue:  "  ON  ",
			wantRequested: true,
			wantEnabled:   true,
		},
		{
			name:          "yes is a valid truthy value in development",
			appEnv:        "development",
			offlineValue:  "YES",
			wantRequested: true,
			wantEnabled:   true,
		},
		{
			name:          "production fails closed",
			appEnv:        "production",
			offlineValue:  "true",
			wantRequested: true,
			wantPolicyErr: true,
		},
		{
			name:          "staging fails closed",
			appEnv:        "staging",
			offlineValue:  "1",
			wantRequested: true,
			wantPolicyErr: true,
		},
		{
			name:          "empty app env fails closed",
			appEnv:        "",
			offlineValue:  "true",
			wantRequested: true,
			wantPolicyErr: true,
		},
		{
			name:          "development-like prefix is not development",
			appEnv:        "development-local",
			offlineValue:  "true",
			wantRequested: true,
			wantPolicyErr: true,
		},
		{
			name:         "unset switch keeps default behavior",
			appEnv:       "development",
			offlineValue: "",
		},
		{
			name:         "falsey values keep default behavior in development",
			appEnv:       "development",
			offlineValue: "false",
		},
		{
			name:         "zero keeps default behavior in development",
			appEnv:       "development",
			offlineValue: "0",
		},
		{
			name:         "off keeps default behavior in development",
			appEnv:       "development",
			offlineValue: "off",
		},
		{
			name:         "garbage value keeps default behavior",
			appEnv:       "development",
			offlineValue: "sometimes",
		},
		{
			name:         "verbose is not an offline truthy value",
			appEnv:       "development",
			offlineValue: "verbose",
		},
		{
			name:         "unset switch keeps default behavior in production",
			appEnv:       "production",
			offlineValue: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("APP_ENV", test.appEnv)
			t.Setenv(OfflineModeEnvKey, test.offlineValue)

			if got := OfflineModeRequested(); got != test.wantRequested {
				t.Fatalf("OfflineModeRequested() = %v, want %v", got, test.wantRequested)
			}

			err := OfflineModePolicyError()
			if test.wantPolicyErr {
				if err == nil {
					t.Fatalf("OfflineModePolicyError() = nil, want a fail-closed error")
				}
				for _, token := range []string{OfflineModeEnvKey, "APP_ENV=development"} {
					if !strings.Contains(err.Error(), token) {
						t.Fatalf("policy error %q must mention %q", err.Error(), token)
					}
				}
			} else if err != nil {
				t.Fatalf("OfflineModePolicyError() = %v, want nil", err)
			}

			if got := OfflineModeEnabled(); got != test.wantEnabled {
				t.Fatalf("OfflineModeEnabled() = %v, want %v", got, test.wantEnabled)
			}
		})
	}
}

func TestOfflineModeEnabledRequiresBothConditions(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv(OfflineModeEnvKey, "true")
	if OfflineModeEnabled() {
		t.Fatal("offline mode must stay disabled when the policy fails closed in production")
	}
	if !OfflineModeRequested() {
		t.Fatal("OfflineModeRequested must still report the operator request in production")
	}

	t.Setenv("APP_ENV", "development")
	if !OfflineModeEnabled() {
		t.Fatal("offline mode must be enabled for explicit development + truthy switch")
	}
}
