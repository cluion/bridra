package framework_test

import (
	"context"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

type publicResourceResolver struct {
	lease *publicResourceLease
}

func (resolver *publicResourceResolver) ResolveBookmark(
	[]byte,
	framework.ResourceBookmarkScope,
) (framework.ResourceLease, error) {
	resolver.lease = &publicResourceLease{path: "/public/resource"}
	return resolver.lease, nil
}

type publicResourceLease struct {
	path     string
	released bool
}

func (lease *publicResourceLease) LocalPath() string { return lease.path }

func (lease *publicResourceLease) Release() error {
	lease.released = true
	return nil
}

func TestPublicResourceBrokerProviderAPI(t *testing.T) {
	resolver := &publicResourceResolver{}
	provider := framework.NewResourceBrokerServiceProvider(
		resolver,
		framework.DefaultResourceBrokerOptions(),
	)
	var _ framework.ServiceProvider = provider
	var _ framework.TerminableServiceProvider = provider

	application := framework.NewApplication(nil)
	if err := application.Register(provider); err != nil {
		t.Fatalf("register: %v", err)
	}
	broker, err := framework.Resolve(application.Container(), framework.ResourceBrokerKey)
	if err != nil {
		t.Fatalf("resolve broker: %v", err)
	}
	capability, err := broker.Grant(
		[]byte("opaque-bookmark"),
		framework.ResourceBookmarkEphemeral,
	)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	path, err := broker.ResolvePath(capability)
	if err != nil || path != "/public/resource" {
		t.Fatalf("path = %q, %v", path, err)
	}
	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if resolver.lease == nil || !resolver.lease.released {
		t.Fatal("application shutdown did not release resource lease")
	}
}
