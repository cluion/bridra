package framework

import (
	"errors"
	"testing"
)

func TestConfigUsesTypedDefaultsAndOverrides(t *testing.T) {
	config := NewConfig()
	port := NewConfigKey("server.port", 8080)
	name := NewConfigKey("server.name", "Bridra")

	if got := ConfigValue(config, port); got != 8080 {
		t.Fatalf("default port = %d", got)
	}
	if err := SetConfig(config, port, 9090); err != nil {
		t.Fatalf("set port: %v", err)
	}
	if got := ConfigValue(config, port); got != 9090 {
		t.Fatalf("port = %d", got)
	}
	if !HasConfig(config, port) || HasConfig(config, name) {
		t.Fatalf("unexpected config presence")
	}
}

func TestConfigFreezesAfterBoot(t *testing.T) {
	config := NewConfig()
	key := NewConfigKey("app.name", "Bridra")
	config.Freeze()

	if err := SetConfig(config, key, "Changed"); !errors.Is(err, ErrConfigFrozen) {
		t.Fatalf("error = %v, want ErrConfigFrozen", err)
	}
	if got := ConfigValue(config, key); got != "Bridra" {
		t.Fatalf("name = %q", got)
	}
}

func TestConfigRejectsInvalidUsage(t *testing.T) {
	var key ConfigKey[string]
	if err := SetConfig[string](nil, key, "value"); !errors.Is(err, ErrConfigUnavailable) {
		t.Fatalf("nil config error = %v", err)
	}
	if err := SetConfig(NewConfig(), key, "value"); !errors.Is(err, ErrInvalidConfigKey) {
		t.Fatalf("zero key error = %v", err)
	}
}
