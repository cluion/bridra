package providers

import (
	"context"
	"testing"

	"github.com/cluion/bridra/backend/app/contracts"
	"github.com/cluion/bridra/backend/app/responses"
	"github.com/cluion/bridra/backend/app/settings"
	"github.com/cluion/bridra/backend/framework"
)

func TestApplicationServiceProviderRegistersServicesAndRoutes(t *testing.T) {
	config, err := settings.New("secret", nil, "Provider test")
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	application := framework.NewApplication(config)
	if err := application.Register(NewApplicationServiceProvider()); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := application.Boot(); err != nil {
		t.Fatalf("boot: %v", err)
	}

	if _, err := framework.Resolve(application.Container(), GreetingServiceKey); err != nil {
		t.Fatalf("resolve greeting service: %v", err)
	}
	response := application.Router().Dispatch(context.Background(), framework.Request{
		ID:     "1",
		Method: contracts.MethodSystemHealth,
		Meta:   map[string]string{"token": "secret"},
	})
	if response.Error != nil {
		t.Fatalf("response error: %v", response.Error)
	}
	health := response.Result.(responses.HealthResponse)
	if health.Runtime != "Provider test" {
		t.Fatalf("runtime = %#v", health.Runtime)
	}
}
