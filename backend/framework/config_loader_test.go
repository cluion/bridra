package framework

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestConfigLoaderAppliesDefaultsEnvironmentAndRuntimePrecedence(t *testing.T) {
	port := NewConfigKey("server.port", 8080)
	debug := NewConfigKey("server.debug", false)
	timeout := NewConfigKey("server.timeout", time.Second)
	loader := NewConfigLoader(
		IntConfig(port),
		BoolConfig(debug),
		DurationConfig(timeout),
	)
	environment := NewEnvironmentConfigSourceWithLookup(
		"environment",
		"BRIDRA_",
		func(name string) (string, bool) {
			values := map[string]string{
				"BRIDRA_SERVER_PORT":    "9000",
				"BRIDRA_SERVER_DEBUG":   "true",
				"BRIDRA_SERVER_TIMEOUT": "3s",
			}
			value, exists := values[name]
			return value, exists
		},
	)
	runtime := NewMapConfigSource("runtime", map[string]any{"server.port": 9100})

	config, err := loader.Load(environment, runtime)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := ConfigValue(config, port); got != 9100 {
		t.Fatalf("port = %d", got)
	}
	if got := ConfigValue(config, debug); !got {
		t.Fatal("debug should come from environment")
	}
	if got := ConfigValue(config, timeout); got != 3*time.Second {
		t.Fatalf("timeout = %s", got)
	}

	entries := config.Entries()
	sources := make(map[string]string, len(entries))
	for _, entry := range entries {
		sources[entry.Name] = entry.Source
	}
	wantSources := map[string]string{
		"server.debug": "environment", "server.port": "runtime", "server.timeout": "environment",
	}
	if !reflect.DeepEqual(sources, wantSources) {
		t.Fatalf("sources = %#v", sources)
	}
}

func TestConfigLoaderAggregatesDecodeAndValidationErrors(t *testing.T) {
	port := NewConfigKey("server.port", 8080)
	name := NewConfigKey("server.name", "")
	loader := NewConfigLoader(
		IntConfig(port),
		StringConfig(name, RequiredString("is required")),
	)
	source := NewMapConfigSource("environment", map[string]any{
		"server.port": "not-a-port",
		"server.name": " ",
	})

	config, err := loader.Load(source)
	if config != nil {
		t.Fatalf("config = %#v, want nil", config)
	}
	var loadErrors *ConfigLoadErrors
	if !errors.As(err, &loadErrors) {
		t.Fatalf("error = %v, want ConfigLoadErrors", err)
	}
	if len(loadErrors.Violations) != 2 {
		t.Fatalf("violations = %#v", loadErrors.Violations)
	}
	if loadErrors.Violations[0].Key != "server.port" ||
		loadErrors.Violations[1].Key != "server.name" {
		t.Fatalf("violations = %#v", loadErrors.Violations)
	}
}

func TestConfigEntriesRedactSecretsWithoutChangingTypedAccess(t *testing.T) {
	token := NewSecretConfigKey("backend.token", "")
	loader := NewConfigLoader(StringConfig(token, RequiredString("is required")))
	config, err := loader.Load(NewMapConfigSource("runtime", map[string]any{
		"backend.token": "super-secret",
	}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := ConfigValue(config, token); got != "super-secret" {
		t.Fatalf("token = %q", got)
	}
	entries := config.Entries()
	if len(entries) != 1 || entries[0].Value != RedactedConfigValue || !entries[0].Secret {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestSecretDecodeErrorsDoNotExposeRawValues(t *testing.T) {
	secretNumber := NewSecretConfigKey("secret.number", 0)
	loader := NewConfigLoader(IntConfig(secretNumber))

	_, err := loader.Load(NewMapConfigSource("environment", map[string]any{
		"secret.number": "do-not-leak",
	}))

	var loadErrors *ConfigLoadErrors
	if !errors.As(err, &loadErrors) {
		t.Fatalf("error = %v", err)
	}
	if loadErrors.Violations[0].Message != "value is invalid" {
		t.Fatalf("violation = %#v", loadErrors.Violations[0])
	}
}

func TestSecretValidatorErrorsDoNotExposeRawValues(t *testing.T) {
	token := NewSecretConfigKey("backend.token", "")
	loader := NewConfigLoader(StringConfig(token, func(value string) error {
		return errors.New("rejected token " + value)
	}))

	_, err := loader.Load(NewMapConfigSource("runtime", map[string]any{
		"backend.token": "do-not-leak",
	}))

	var loadErrors *ConfigLoadErrors
	if !errors.As(err, &loadErrors) {
		t.Fatalf("error = %v", err)
	}
	if loadErrors.Violations[0].Message != "value is invalid" {
		t.Fatalf("violation = %#v", loadErrors.Violations[0])
	}
}

func TestConfigLoaderRejectsDuplicateSettingsAtomically(t *testing.T) {
	port := NewConfigKey("server.port", 8080)
	name := NewConfigKey("server.name", "Bridra")
	loader := NewConfigLoader(IntConfig(port))

	err := loader.Register(StringConfig(name), IntConfig(port))
	if !errors.Is(err, ErrConfigSettingAlreadyExists) {
		t.Fatalf("error = %v, want ErrConfigSettingAlreadyExists", err)
	}
	config, loadErr := loader.Load()
	if loadErr != nil {
		t.Fatalf("load: %v", loadErr)
	}
	if len(config.Entries()) != 1 {
		t.Fatalf("failed registration partially mutated loader: %#v", config.Entries())
	}
}

func TestConfigLoaderRejectsTypedNilSource(t *testing.T) {
	loader := NewConfigLoader(StringConfig(NewConfigKey("app.name", "Bridra")))
	var source *MapConfigSource

	if _, err := loader.Load(source); !errors.Is(err, ErrInvalidConfigSource) {
		t.Fatalf("error = %v, want ErrInvalidConfigSource", err)
	}
}
