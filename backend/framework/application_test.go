package framework

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

var lifecycleName = NewConfigKey("test.name", "default")
var lifecycleService = NewServiceKey[string]("test.service")

type lifecycleProvider struct {
	events *[]string
}

func (provider lifecycleProvider) Register(application *Application) error {
	*provider.events = append(*provider.events, "register")
	return Provide(application.Container(), lifecycleService, func(*Container) (string, error) {
		return ConfigValue(application.Config(), lifecycleName), nil
	})
}

func (provider lifecycleProvider) Boot(application *Application) error {
	*provider.events = append(*provider.events, "boot")
	service, err := Resolve(application.Container(), lifecycleService)
	if err != nil {
		return err
	}
	application.Router().Handle("test.lifecycle", func(*Context) (any, error) {
		return service, nil
	})
	return nil
}

func TestApplicationRunsProviderLifecycleAndFreezesConfig(t *testing.T) {
	config := NewConfig()
	if err := SetConfig(config, lifecycleName, "Bridra"); err != nil {
		t.Fatalf("set config: %v", err)
	}
	application := NewApplication(config)
	events := []string{}

	if err := application.Register(lifecycleProvider{events: &events}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := application.Boot(); err != nil {
		t.Fatalf("boot: %v", err)
	}
	if err := application.Boot(); err != nil {
		t.Fatalf("second boot: %v", err)
	}

	if !reflect.DeepEqual(events, []string{"register", "boot"}) {
		t.Fatalf("events = %#v", events)
	}
	if !application.Booted() || !config.Frozen() {
		t.Fatal("application should be booted with frozen config")
	}
	response := application.Router().Dispatch(context.Background(), Request{
		ID: "1", Method: "test.lifecycle",
	})
	if response.Error != nil || response.Result != "Bridra" {
		t.Fatalf("response = %#v", response)
	}
}

func TestApplicationRejectsProvidersAfterBoot(t *testing.T) {
	application := NewApplication(nil)
	if err := application.Boot(); err != nil {
		t.Fatalf("boot: %v", err)
	}

	err := application.Register(lifecycleProvider{events: &[]string{}})
	if !errors.Is(err, ErrApplicationBooted) {
		t.Fatalf("error = %v, want ErrApplicationBooted", err)
	}
}

type failingProvider struct {
	err error
}

func (provider failingProvider) Register(*Application) error {
	return provider.err
}

func TestApplicationWrapsProviderErrors(t *testing.T) {
	application := NewApplication(nil)
	providerError := errors.New("provider failed")

	err := application.Register(failingProvider{err: providerError})

	if !errors.Is(err, providerError) {
		t.Fatalf("error = %v", err)
	}
}

type reentrantProvider struct {
	lifecycleError error
}

func (provider *reentrantProvider) Register(application *Application) error {
	provider.lifecycleError = application.Boot()
	return nil
}

func TestApplicationRejectsReentrantLifecycleChangesWithoutDeadlock(t *testing.T) {
	application := NewApplication(nil)
	provider := &reentrantProvider{}

	if err := application.Register(provider); err != nil {
		t.Fatalf("register: %v", err)
	}
	if !errors.Is(provider.lifecycleError, ErrApplicationBusy) {
		t.Fatalf("lifecycle error = %v, want ErrApplicationBusy", provider.lifecycleError)
	}
	if err := application.Boot(); err != nil {
		t.Fatalf("boot: %v", err)
	}
}

func TestApplicationRejectsTypedNilProviders(t *testing.T) {
	application := NewApplication(nil)
	var provider *lifecycleProvider

	if err := application.Register(provider); err == nil {
		t.Fatal("expected typed nil provider error")
	}
}
