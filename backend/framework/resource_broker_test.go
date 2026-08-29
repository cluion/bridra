package framework

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
)

type resourceBrokerTestResolver struct {
	mu        sync.Mutex
	path      string
	err       error
	started   chan struct{}
	continueC chan struct{}
	leases    []*resourceBrokerTestLease
	scopes    []ResourceBookmarkScope
}

func (resolver *resourceBrokerTestResolver) ResolveBookmark(
	_ []byte,
	scope ResourceBookmarkScope,
) (ResourceLease, error) {
	if resolver.started != nil {
		close(resolver.started)
	}
	if resolver.continueC != nil {
		<-resolver.continueC
	}
	if resolver.err != nil {
		return nil, resolver.err
	}
	lease := &resourceBrokerTestLease{path: resolver.path}
	resolver.mu.Lock()
	resolver.leases = append(resolver.leases, lease)
	resolver.scopes = append(resolver.scopes, scope)
	resolver.mu.Unlock()
	return lease, nil
}

type resourceBrokerTestLease struct {
	path       string
	releaseErr error
	mu         sync.Mutex
	releases   int
}

func (lease *resourceBrokerTestLease) LocalPath() string {
	return lease.path
}

func (lease *resourceBrokerTestLease) Release() error {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	lease.releases++
	return lease.releaseErr
}

func (lease *resourceBrokerTestLease) releaseCount() int {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	return lease.releases
}

func TestResourceBrokerGrantResolveAndIdempotentRelease(t *testing.T) {
	resolver := &resourceBrokerTestResolver{path: "/private/resource"}
	broker, err := NewResourceBroker(resolver, DefaultResourceBrokerOptions())
	if err != nil {
		t.Fatalf("new broker: %v", err)
	}

	first, err := broker.Grant([]byte("bookmark-one"), ResourceBookmarkEphemeral)
	if err != nil {
		t.Fatalf("grant first: %v", err)
	}
	second, err := broker.Grant([]byte("bookmark-two"), ResourceBookmarkPersistent)
	if err != nil {
		t.Fatalf("grant second: %v", err)
	}
	if first == second || !validResourceCapability(first) || !validResourceCapability(second) {
		t.Fatalf("capabilities are not distinct opaque 256-bit values")
	}
	path, err := broker.ResolvePath(first)
	if err != nil || path != resolver.path {
		t.Fatalf("resolve path = %q, %v", path, err)
	}
	if err := broker.Release(first); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := broker.Release(first); err != nil {
		t.Fatalf("duplicate release: %v", err)
	}
	if _, err := broker.ResolvePath(first); !errors.Is(err, ErrResourceCapabilityNotFound) {
		t.Fatalf("resolved released capability: %v", err)
	}
	if resolver.leases[0].releaseCount() != 1 {
		t.Fatalf("first lease releases = %d", resolver.leases[0].releaseCount())
	}
	if err := broker.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if resolver.leases[1].releaseCount() != 1 {
		t.Fatalf("second lease releases = %d", resolver.leases[1].releaseCount())
	}
	if len(resolver.scopes) != 2 || resolver.scopes[0] != ResourceBookmarkEphemeral ||
		resolver.scopes[1] != ResourceBookmarkPersistent {
		t.Fatalf("scopes = %#v", resolver.scopes)
	}
}

func TestResourceBrokerValidatesBoundsAndCapabilities(t *testing.T) {
	resolver := &resourceBrokerTestResolver{path: "/private/resource"}
	broker, err := NewResourceBroker(resolver, ResourceBrokerOptions{
		MaxBookmarkBytes: 4,
		MaxActiveGrants:  1,
	})
	if err != nil {
		t.Fatalf("new broker: %v", err)
	}
	t.Cleanup(func() { _ = broker.Close() })

	if _, err := broker.Grant(nil, ResourceBookmarkEphemeral); !errors.Is(err, ErrResourceBookmarkInvalid) {
		t.Fatalf("empty bookmark error = %v", err)
	}
	if _, err := broker.Grant([]byte("12345"), ResourceBookmarkEphemeral); !errors.Is(err, ErrResourceBookmarkTooLarge) {
		t.Fatalf("oversized bookmark error = %v", err)
	}
	if _, err := broker.Grant([]byte("1234"), "future"); !errors.Is(err, ErrResourceBookmarkInvalid) {
		t.Fatalf("scope error = %v", err)
	}
	capability, err := broker.Grant([]byte("1234"), ResourceBookmarkEphemeral)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if _, err := broker.Grant([]byte("more"), ResourceBookmarkEphemeral); !errors.Is(err, ErrResourceGrantLimit) {
		t.Fatalf("limit error = %v", err)
	}
	if _, err := broker.ResolvePath("secret"); !errors.Is(err, ErrResourceCapabilityInvalid) {
		t.Fatalf("invalid capability error = %v", err)
	}
	unknown := ResourceCapability(strings.Repeat("a", resourceCapabilityBytes*2))
	if err := broker.Release(unknown); !errors.Is(err, ErrResourceCapabilityNotFound) {
		t.Fatalf("unknown capability error = %v", err)
	}
	if err := broker.Release(capability); err != nil {
		t.Fatalf("release: %v", err)
	}
}

func TestResourceBrokerCloseWaitsForGrantAndReleasesIt(t *testing.T) {
	started := make(chan struct{})
	continueC := make(chan struct{})
	resolver := &resourceBrokerTestResolver{
		path:      "/private/resource",
		started:   started,
		continueC: continueC,
	}
	broker, err := NewResourceBroker(resolver, DefaultResourceBrokerOptions())
	if err != nil {
		t.Fatalf("new broker: %v", err)
	}
	grantDone := make(chan error, 1)
	go func() {
		_, err := broker.Grant([]byte("bookmark"), ResourceBookmarkEphemeral)
		grantDone <- err
	}()
	<-started
	closeDone := make(chan error, 1)
	go func() { closeDone <- broker.Close() }()
	for {
		broker.mu.Lock()
		closed := broker.closed
		broker.mu.Unlock()
		if closed {
			break
		}
		runtime.Gosched()
	}
	close(continueC)

	if err := <-grantDone; !errors.Is(err, ErrResourceBrokerClosed) {
		t.Fatalf("grant error = %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("close: %v", err)
	}
	if len(resolver.leases) != 1 || resolver.leases[0].releaseCount() != 1 {
		t.Fatalf("leases = %#v", resolver.leases)
	}
	if err := broker.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestResourceBrokerCloseReportsRedactedReleaseFailure(t *testing.T) {
	resolver := &resourceBrokerTestResolver{path: "/private/secret"}
	broker, err := NewResourceBroker(resolver, DefaultResourceBrokerOptions())
	if err != nil {
		t.Fatalf("new broker: %v", err)
	}
	if _, err := broker.Grant([]byte("secret-bookmark"), ResourceBookmarkEphemeral); err != nil {
		t.Fatalf("grant: %v", err)
	}
	resolver.leases[0].releaseErr = errors.New("/private/secret")
	err = broker.Close()
	if !errors.Is(err, ErrResourceLeaseRelease) {
		t.Fatalf("close error = %v", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("close error exposed resource data: %v", err)
	}
}

func TestResourceBrokerRedactsResolverFailure(t *testing.T) {
	resolver := &resourceBrokerTestResolver{err: errors.New("/private/secret")}
	broker, err := NewResourceBroker(resolver, DefaultResourceBrokerOptions())
	if err != nil {
		t.Fatalf("new broker: %v", err)
	}
	t.Cleanup(func() { _ = broker.Close() })

	_, err = broker.Grant([]byte("secret-bookmark"), ResourceBookmarkEphemeral)
	if !errors.Is(err, ErrResourceBookmarkResolve) {
		t.Fatalf("grant error = %v", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("grant error exposed resource data: %v", err)
	}
}

func TestResourceBrokerServiceProviderClosesLeases(t *testing.T) {
	resolver := &resourceBrokerTestResolver{path: "/private/resource"}
	provider := NewResourceBrokerServiceProvider(resolver, DefaultResourceBrokerOptions())
	application := NewApplication(nil)
	if err := application.Register(provider); err != nil {
		t.Fatalf("register: %v", err)
	}
	broker, err := Resolve(application.Container(), ResourceBrokerKey)
	if err != nil {
		t.Fatalf("resolve broker: %v", err)
	}
	if _, err := broker.Grant([]byte("bookmark"), ResourceBookmarkEphemeral); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if resolver.leases[0].releaseCount() != 1 {
		t.Fatalf("lease releases = %d", resolver.leases[0].releaseCount())
	}
}

func TestNewResourceBrokerRejectsNilResolverAndInvalidOptions(t *testing.T) {
	if _, err := NewResourceBroker(nil, ResourceBrokerOptions{}); !errors.Is(err, ErrResourceBrokerOptions) {
		t.Fatalf("nil resolver error = %v", err)
	}
	var resolver *resourceBrokerTestResolver
	if _, err := NewResourceBroker(resolver, ResourceBrokerOptions{}); !errors.Is(err, ErrResourceBrokerOptions) {
		t.Fatalf("typed nil resolver error = %v", err)
	}
	valid := &resourceBrokerTestResolver{path: "/private/resource"}
	if _, err := NewResourceBroker(valid, ResourceBrokerOptions{MaxBookmarkBytes: -1}); !errors.Is(err, ErrResourceBrokerOptions) {
		t.Fatalf("invalid options error = %v", err)
	}
}
