package framework

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type containerV2Service struct {
	id int32
}

func TestLazySingletonBuildsOnceAcrossConcurrentResolvers(t *testing.T) {
	container := NewContainer()
	key := NewServiceKey[*containerV2Service]("v2.singleton")
	var builds atomic.Int32
	if err := BindSingleton(container, key, func(Resolver) (*containerV2Service, error) {
		return &containerV2Service{id: builds.Add(1)}, nil
	}); err != nil {
		t.Fatalf("bind singleton: %v", err)
	}
	if builds.Load() != 0 {
		t.Fatal("singleton factory ran before resolution")
	}

	const resolverCount = 32
	services := make([]*containerV2Service, resolverCount)
	errorsByResolver := make([]error, resolverCount)
	var wait sync.WaitGroup
	for index := range services {
		wait.Add(1)
		go func() {
			defer wait.Done()
			services[index], errorsByResolver[index] = Resolve(container, key)
		}()
	}
	wait.Wait()

	for index, err := range errorsByResolver {
		if err != nil {
			t.Fatalf("resolve %d: %v", index, err)
		}
		if services[index] != services[0] {
			t.Fatalf("resolver %d received a different singleton", index)
		}
	}
	if builds.Load() != 1 {
		t.Fatalf("singleton factory calls = %d", builds.Load())
	}
}

func TestTransientBuildsEveryResolution(t *testing.T) {
	container := NewContainer()
	key := NewServiceKey[*containerV2Service]("v2.transient")
	var builds atomic.Int32
	if err := BindTransient(container, key, func(Resolver) (*containerV2Service, error) {
		return &containerV2Service{id: builds.Add(1)}, nil
	}); err != nil {
		t.Fatalf("bind transient: %v", err)
	}

	first, err := Resolve(container, key)
	if err != nil {
		t.Fatalf("resolve first: %v", err)
	}
	second, err := Resolve(container, key)
	if err != nil {
		t.Fatalf("resolve second: %v", err)
	}
	if first == second || first.id != 1 || second.id != 2 {
		t.Fatalf("transient services = %#v, %#v", first, second)
	}
}

func TestScopedServiceIsSharedOnlyWithinOneScope(t *testing.T) {
	container := NewContainer()
	key := NewServiceKey[*containerV2Service]("v2.scoped")
	var builds atomic.Int32
	if err := BindScoped(container, key, func(Resolver) (*containerV2Service, error) {
		return &containerV2Service{id: builds.Add(1)}, nil
	}); err != nil {
		t.Fatalf("bind scoped: %v", err)
	}

	if _, err := Resolve(container, key); !errors.Is(err, ErrScopeRequired) {
		t.Fatalf("root resolve error = %v, want ErrScopeRequired", err)
	}
	firstScope := container.NewScope()
	first, err := Resolve(firstScope, key)
	if err != nil {
		t.Fatalf("resolve first: %v", err)
	}
	again, err := Resolve(firstScope, key)
	if err != nil {
		t.Fatalf("resolve again: %v", err)
	}
	second, err := Resolve(container.NewScope(), key)
	if err != nil {
		t.Fatalf("resolve second scope: %v", err)
	}
	if first != again {
		t.Fatal("scope did not reuse its service")
	}
	if first == second || first.id != 1 || second.id != 2 {
		t.Fatalf("scoped services = %#v, %#v", first, second)
	}
}

func TestAliasResolvesConcreteServiceAsInterface(t *testing.T) {
	container := NewContainer()
	concreteKey := NewServiceKey[namedServiceValue]("v2.named.concrete")
	interfaceKey := NewServiceKey[namedService]("v2.named")
	if err := Instance(container, concreteKey, namedServiceValue{name: "Bridra"}); err != nil {
		t.Fatalf("instance: %v", err)
	}
	if err := Alias(container, interfaceKey, concreteKey); err != nil {
		t.Fatalf("alias: %v", err)
	}

	service, err := Resolve(container, interfaceKey)
	if err != nil {
		t.Fatalf("resolve alias: %v", err)
	}
	if service.Name() != "Bridra" || !HasService(container, interfaceKey) {
		t.Fatalf("service = %#v", service)
	}
}

func TestAliasRejectsMissingAndIncompatibleTargets(t *testing.T) {
	container := NewContainer()
	interfaceKey := NewServiceKey[namedService]("v2.alias")
	missingKey := NewServiceKey[namedServiceValue]("v2.missing")
	if err := Alias(container, interfaceKey, missingKey); !errors.Is(err, ErrServiceNotFound) {
		t.Fatalf("missing target error = %v", err)
	}

	stringKey := NewServiceKey[string]("v2.string")
	if err := Instance(container, stringKey, "value"); err != nil {
		t.Fatalf("string instance: %v", err)
	}
	if err := Alias(container, interfaceKey, stringKey); !errors.Is(err, ErrInvalidServiceAlias) {
		t.Fatalf("incompatible alias error = %v", err)
	}
}

func TestContainerDetectsCircularDependencies(t *testing.T) {
	container := NewContainer()
	firstKey := NewServiceKey[string]("v2.first")
	secondKey := NewServiceKey[string]("v2.second")
	if err := BindTransient(container, firstKey, func(resolver Resolver) (string, error) {
		return Resolve(resolver, secondKey)
	}); err != nil {
		t.Fatalf("bind first: %v", err)
	}
	if err := BindTransient(container, secondKey, func(resolver Resolver) (string, error) {
		return Resolve(resolver, firstKey)
	}); err != nil {
		t.Fatalf("bind second: %v", err)
	}

	_, err := Resolve(container, firstKey)
	if !errors.Is(err, ErrCircularDependency) {
		t.Fatalf("resolve error = %v, want ErrCircularDependency", err)
	}
	wantPath := "v2.first -> v2.second -> v2.first"
	if !strings.Contains(err.Error(), wantPath) {
		t.Fatalf("resolve error = %v, want path %q", err, wantPath)
	}
}

func TestSingletonCannotCaptureScopedService(t *testing.T) {
	container := NewContainer()
	scopedKey := NewServiceKey[*containerV2Service]("v2.request")
	singletonKey := NewServiceKey[*containerV2Service]("v2.global")
	if err := BindScoped(container, scopedKey, func(Resolver) (*containerV2Service, error) {
		return &containerV2Service{id: 1}, nil
	}); err != nil {
		t.Fatalf("bind scoped: %v", err)
	}
	if err := BindSingleton(container, singletonKey, func(resolver Resolver) (*containerV2Service, error) {
		return Resolve(resolver, scopedKey)
	}); err != nil {
		t.Fatalf("bind singleton: %v", err)
	}

	if _, err := Resolve(container.NewScope(), singletonKey); !errors.Is(err, ErrScopedServiceFromSingleton) {
		t.Fatalf("resolve error = %v, want ErrScopedServiceFromSingleton", err)
	}
}

func TestFailedSingletonFactoryCanRetry(t *testing.T) {
	container := NewContainer()
	key := NewServiceKey[string]("v2.retry")
	var attempts int
	if err := BindSingleton(container, key, func(Resolver) (string, error) {
		attempts++
		if attempts == 1 {
			return "", errors.New("not ready")
		}
		return "ready", nil
	}); err != nil {
		t.Fatalf("bind singleton: %v", err)
	}

	if _, err := Resolve(container, key); err == nil {
		t.Fatal("first resolution should fail")
	}
	service, err := Resolve(container, key)
	if err != nil || service != "ready" {
		t.Fatalf("service = %q, error = %v", service, err)
	}
	if attempts != 2 {
		t.Fatalf("factory attempts = %d", attempts)
	}
}
