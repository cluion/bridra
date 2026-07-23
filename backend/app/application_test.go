package app

import (
	"context"
	"errors"
	"testing"

	appevents "github.com/cluion/bridra/backend/app/events"
	"github.com/cluion/bridra/backend/app/providers"
	"github.com/cluion/bridra/backend/app/responses"
	"github.com/cluion/bridra/backend/app/settings"

	"github.com/cluion/bridra/backend/framework"
)

func TestApplicationRouterServesVersionedHealthWithNilLogs(t *testing.T) {
	router := NewRouter("secret", nil)
	response := router.Dispatch(context.Background(), framework.Request{
		ID:     "1",
		Method: "system.health",
		Meta:   map[string]string{"token": "secret"},
	})

	if response.Error != nil {
		t.Fatalf("unexpected error: %v", response.Error)
	}
	health := response.Result.(responses.HealthResponse)
	if health.FrameworkVersion != framework.FrameworkVersion {
		t.Fatalf("frameworkVersion = %#v", health.FrameworkVersion)
	}
	if health.ProtocolVersion != framework.ProtocolVersion {
		t.Fatalf("protocolVersion = %#v", health.ProtocolVersion)
	}
}

type greetingEventProvider struct {
	events []appevents.GreetingCreated
}

func (provider *greetingEventProvider) Register(application *framework.Application) error {
	return framework.Listen(
		application.Events(),
		"test.greeting-capture",
		func(_ context.Context, event appevents.GreetingCreated) error {
			provider.events = append(provider.events, event)
			return nil
		},
	)
}

func TestAdditionalProviderCanListenToApplicationEvents(t *testing.T) {
	provider := &greetingEventProvider{}
	application, err := Build(Config{Token: "secret"}, provider)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	response := application.Router().Dispatch(context.Background(), framework.Request{
		ID: "1", Method: "greeting.hello", Params: []byte(`{"name":"Codex"}`),
		Meta: map[string]string{"token": "secret"},
	})
	if response.Error != nil {
		t.Fatalf("response error: %v", response.Error)
	}
	if len(provider.events) != 1 || provider.events[0].Greeting.Message != "Hello, Codex!" {
		t.Fatalf("events = %#v", provider.events)
	}
}

func TestApplicationReportsConfiguredRuntime(t *testing.T) {
	router := New(Config{Token: "secret", Runtime: "Go HTTP server"})
	response := router.Dispatch(context.Background(), framework.Request{
		ID:     "1",
		Method: "system.health",
		Meta:   map[string]string{"token": "secret"},
	})

	if response.Error != nil {
		t.Fatalf("unexpected error: %v", response.Error)
	}
	health := response.Result.(responses.HealthResponse)
	if health.Runtime != "Go HTTP server" {
		t.Fatalf("runtime = %#v", health.Runtime)
	}
}

func TestBuildBootsProvidersAndFreezesConfiguration(t *testing.T) {
	application, err := Build(Config{Token: "secret"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !application.Booted() {
		t.Fatal("application should be booted")
	}
	if _, err := framework.Resolve(
		application.Container(),
		providers.GreetingServiceKey,
	); err != nil {
		t.Fatalf("resolve greeting service: %v", err)
	}

	err = framework.SetConfig(application.Config(), settings.RuntimeName, "changed")
	if !errors.Is(err, framework.ErrConfigFrozen) {
		t.Fatalf("config error = %v, want ErrConfigFrozen", err)
	}
}

type trackingProvider struct {
	registered bool
	booted     bool
}

type failingBuildProvider struct {
	registerErr error
	bootErr     error
	shutdownErr error
	terminated  bool
}

func (provider *failingBuildProvider) Register(*framework.Application) error {
	return provider.registerErr
}

func (provider *failingBuildProvider) Boot(*framework.Application) error {
	return provider.bootErr
}

func (provider *failingBuildProvider) Terminate(
	context.Context,
	*framework.Application,
) error {
	provider.terminated = true
	return provider.shutdownErr
}

func (provider *trackingProvider) Register(*framework.Application) error {
	provider.registered = true
	return nil
}

func (provider *trackingProvider) Boot(*framework.Application) error {
	provider.booted = true
	return nil
}

func TestBuildAcceptsAdditionalServiceProviders(t *testing.T) {
	provider := &trackingProvider{}
	if _, err := Build(Config{Token: "secret"}, provider); err != nil {
		t.Fatalf("build: %v", err)
	}
	if !provider.registered || !provider.booted {
		t.Fatalf("provider lifecycle = registered:%v booted:%v", provider.registered, provider.booted)
	}
}

func TestBuildCleansUpProviderAfterPartialStartupFailure(t *testing.T) {
	registerError := errors.New("register failed")
	shutdownError := errors.New("shutdown failed")
	provider := &failingBuildProvider{
		registerErr: registerError,
		shutdownErr: shutdownError,
	}

	application, err := Build(Config{Token: "secret"}, provider)
	if application != nil {
		t.Fatal("failed build should not return an application")
	}
	if !errors.Is(err, registerError) || !errors.Is(err, shutdownError) {
		t.Fatalf("build error = %v, want startup and shutdown errors", err)
	}
	if !provider.terminated {
		t.Fatal("provider should terminate after partial startup failure")
	}
}

func TestBuildCleansUpProviderAfterBootFailure(t *testing.T) {
	bootError := errors.New("boot failed")
	provider := &failingBuildProvider{bootErr: bootError}

	application, err := Build(Config{Token: "secret"}, provider)
	if application != nil {
		t.Fatal("failed build should not return an application")
	}
	if !errors.Is(err, bootError) {
		t.Fatalf("build error = %v, want %v", err, bootError)
	}
	if !provider.terminated {
		t.Fatal("provider should terminate after boot failure")
	}
}

func TestBuildFromSourcesUsesEnvironmentThenRuntimePrecedence(t *testing.T) {
	environment := framework.NewEnvironmentConfigSourceWithLookup(
		"environment",
		"BRIDRA_",
		func(name string) (string, bool) {
			if name == "BRIDRA_BACKEND_TOKEN" {
				return "environment-token", true
			}
			return "", false
		},
	)
	runtime := framework.NewMapConfigSource("runtime", map[string]any{
		settings.BackendToken.Name(): "runtime-token",
		settings.RuntimeName.Name():  "Runtime override",
	})

	application, err := BuildFromSources([]framework.ConfigSource{environment, runtime})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	response := application.Router().Dispatch(context.Background(), framework.Request{
		ID: "1", Method: "system.health", Meta: map[string]string{"token": "runtime-token"},
	})
	if response.Error != nil {
		t.Fatalf("response error: %v", response.Error)
	}
	health := response.Result.(responses.HealthResponse)
	if health.Runtime != "Runtime override" {
		t.Fatalf("runtime = %q", health.Runtime)
	}
}
