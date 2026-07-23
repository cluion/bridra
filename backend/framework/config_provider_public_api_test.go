package framework_test

import (
	"errors"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

type publicManifestProvider struct {
	registered bool
	booted     bool
}

func (provider *publicManifestProvider) Register(*framework.Application) error {
	provider.registered = true
	return nil
}

func (provider *publicManifestProvider) Boot(*framework.Application) error {
	provider.booted = true
	return nil
}

func TestPublicConfigSourcesAndProviderManifest(t *testing.T) {
	token := framework.NewSecretConfigKey("public.token", "")
	workers := framework.NewConfigKey("public.workers", 2)
	loader := framework.NewConfigLoader(
		framework.StringConfig(token, framework.RequiredString("is required")),
		framework.IntConfig(workers),
	)
	config, err := loader.Load(
		framework.NewEnvironmentConfigSourceWithLookup(
			"environment",
			"PUBLIC_",
			func(name string) (string, bool) {
				values := map[string]string{
					"PUBLIC_PUBLIC_TOKEN":   "secret",
					"PUBLIC_PUBLIC_WORKERS": "4",
				}
				value, exists := values[name]
				return value, exists
			},
		),
		framework.NewMapConfigSource("runtime", map[string]any{"public.workers": 8}),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if framework.ConfigValue(config, workers) != 8 {
		t.Fatal("runtime source did not override environment")
	}
	redacted := false
	for _, entry := range config.Entries() {
		if entry.Name == token.Name() {
			redacted = entry.Value == framework.RedactedConfigValue && entry.Secret
		}
	}
	if !redacted {
		t.Fatalf("entries = %#v", config.Entries())
	}

	provider := &publicManifestProvider{}
	manifest := framework.NewProviderManifest()
	if err := manifest.Add("public.application", provider); err != nil {
		t.Fatalf("add provider: %v", err)
	}
	if err := manifest.Add("public.application", provider); !errors.Is(err, framework.ErrProviderAlreadyDefined) {
		t.Fatalf("duplicate error = %v", err)
	}
	application := framework.NewApplication(config)
	if err := application.RegisterManifest(manifest); err != nil {
		t.Fatalf("register manifest: %v", err)
	}
	if err := application.Boot(); err != nil {
		t.Fatalf("boot: %v", err)
	}
	if !provider.registered || !provider.booted {
		t.Fatalf("provider = %#v", provider)
	}
}
