package settings

import (
	"errors"
	"io"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

func TestNewAppliesApplicationDefaults(t *testing.T) {
	config, err := New("secret", nil, "")
	if err != nil {
		t.Fatalf("new settings: %v", err)
	}

	if token := framework.ConfigValue(config, BackendToken); token != "secret" {
		t.Fatalf("token = %q", token)
	}
	if logs := framework.ConfigValue(config, LogOutput); logs != io.Discard {
		t.Fatalf("logs = %#v", logs)
	}
	if runtime := framework.ConfigValue(config, RuntimeName); runtime != "Go backend" {
		t.Fatalf("runtime = %q", runtime)
	}
}

func TestLoadAppliesEnvironmentAndRuntimePrecedence(t *testing.T) {
	environment := framework.NewEnvironmentConfigSourceWithLookup(
		"environment",
		"BRIDRA_",
		func(name string) (string, bool) {
			values := map[string]string{
				"BRIDRA_BACKEND_TOKEN": "environment-token",
				"BRIDRA_RUNTIME_NAME":  "Environment runtime",
			}
			value, exists := values[name]
			return value, exists
		},
	)
	runtime := framework.NewMapConfigSource("runtime", map[string]any{
		BackendToken.Name(): "runtime-token",
	})

	config, err := Load(environment, runtime)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if token := framework.ConfigValue(config, BackendToken); token != "runtime-token" {
		t.Fatalf("token = %q", token)
	}
	if runtimeName := framework.ConfigValue(config, RuntimeName); runtimeName != "Environment runtime" {
		t.Fatalf("runtime = %q", runtimeName)
	}
	for _, entry := range config.Entries() {
		if entry.Name == BackendToken.Name() && entry.Value != framework.RedactedConfigValue {
			t.Fatalf("token entry = %#v", entry)
		}
	}
}

func TestLoadRequiresBackendToken(t *testing.T) {
	_, err := Load()
	var loadErrors *framework.ConfigLoadErrors
	if !errors.As(err, &loadErrors) {
		t.Fatalf("error = %v, want ConfigLoadErrors", err)
	}
	if len(loadErrors.Violations) != 1 || loadErrors.Violations[0].Key != BackendToken.Name() {
		t.Fatalf("violations = %#v", loadErrors.Violations)
	}
}
