package framework_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

var publicName = framework.NewConfigKey("public.name", "default")
var publicService = framework.NewServiceKey[string]("public.service")

type publicLifecycleProvider struct {
	events *[]string
}

func (provider publicLifecycleProvider) Register(application *framework.Application) error {
	*provider.events = append(*provider.events, "register")
	return framework.Provide(
		application.Container(),
		publicService,
		func(*framework.Container) (string, error) {
			return framework.ConfigValue(application.Config(), publicName), nil
		},
	)
}

func (provider publicLifecycleProvider) Boot(application *framework.Application) error {
	*provider.events = append(*provider.events, "boot")
	service, err := framework.Resolve(application.Container(), publicService)
	if err != nil {
		return err
	}
	application.Router().Handle("public.lifecycle", func(*framework.Context) (any, error) {
		return service, nil
	})
	return nil
}

func TestPublicApplicationLifecycle(t *testing.T) {
	config := framework.NewConfig()
	if err := framework.SetConfig(config, publicName, "Bridra"); err != nil {
		t.Fatalf("set config: %v", err)
	}
	application := framework.NewApplication(config)
	events := []string{}

	if err := application.Register(publicLifecycleProvider{events: &events}); err != nil {
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
	if !application.Booted() || application.Failed() || !config.Frozen() {
		t.Fatal("application should be booted successfully")
	}
	response := application.Router().Dispatch(context.Background(), framework.Request{
		ID: "1", Method: "public.lifecycle",
	})
	if response.Error != nil || response.Result != "Bridra" {
		t.Fatalf("response = %#v", response)
	}
}

type publicBootProvider struct {
	registers int
	boots     int
	method    string
	err       error
}

func (provider *publicBootProvider) Register(*framework.Application) error {
	provider.registers++
	return nil
}

func (provider *publicBootProvider) Boot(application *framework.Application) error {
	provider.boots++
	if provider.method != "" {
		application.Router().Handle(provider.method, func(*framework.Context) (any, error) {
			return "partial", nil
		})
	}
	return provider.err
}

func TestPublicApplicationBootFailureIsTerminal(t *testing.T) {
	providerError := errors.New("provider boot failed")
	partial := &publicBootProvider{method: "public.partial"}
	failing := &publicBootProvider{err: providerError}
	application := framework.NewApplication(nil)

	if err := application.Register(partial, failing); err != nil {
		t.Fatalf("register: %v", err)
	}
	bootError := application.Boot()
	if !errors.Is(bootError, framework.ErrApplicationFailed) {
		t.Fatalf("boot error = %v, want ErrApplicationFailed", bootError)
	}
	if !errors.Is(bootError, providerError) {
		t.Fatalf("boot error = %v, want provider error", bootError)
	}
	if application.Booted() || !application.Failed() {
		t.Fatal("application should be terminally failed")
	}

	retryError := application.Boot()
	if retryError != bootError {
		t.Fatalf("retry error = %v, want original lifecycle error", retryError)
	}
	if partial.boots != 1 || failing.boots != 1 {
		t.Fatalf("boot counts = partial:%d failing:%d", partial.boots, failing.boots)
	}

	additional := &publicBootProvider{}
	registerError := application.Register(additional)
	if registerError != bootError {
		t.Fatalf("register error = %v, want original lifecycle error", registerError)
	}
	if additional.registers != 0 {
		t.Fatal("provider registered after application failed")
	}
}

type publicRegisterProvider struct {
	registers int
	boots     int
	err       error
}

func (provider *publicRegisterProvider) Register(*framework.Application) error {
	provider.registers++
	return provider.err
}

func (provider *publicRegisterProvider) Boot(*framework.Application) error {
	provider.boots++
	return nil
}

func TestPublicApplicationRegisterFailureIsTerminal(t *testing.T) {
	providerError := errors.New("provider registration failed")
	registered := &publicRegisterProvider{}
	failing := &publicRegisterProvider{err: providerError}
	application := framework.NewApplication(nil)

	registerError := application.Register(registered, failing)
	if !errors.Is(registerError, framework.ErrApplicationFailed) {
		t.Fatalf("register error = %v, want ErrApplicationFailed", registerError)
	}
	if !errors.Is(registerError, providerError) {
		t.Fatalf("register error = %v, want provider error", registerError)
	}
	if application.Booted() || !application.Failed() {
		t.Fatal("application should be terminally failed")
	}

	bootError := application.Boot()
	if bootError != registerError {
		t.Fatalf("boot error = %v, want original lifecycle error", bootError)
	}
	if registered.boots != 0 || failing.boots != 0 {
		t.Fatalf("failed application booted providers: %d, %d", registered.boots, failing.boots)
	}

	registerError = application.Register(registered)
	if registerError != bootError {
		t.Fatalf("retry error = %v, want original lifecycle error", registerError)
	}
	if registered.registers != 1 {
		t.Fatalf("successful provider registered %d times", registered.registers)
	}
}
