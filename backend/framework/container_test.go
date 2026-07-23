package framework

import (
	"errors"
	"testing"
)

type namedService interface {
	Name() string
}

type namedServiceValue struct {
	name string
}

func (service namedServiceValue) Name() string {
	return service.name
}

func TestContainerProvidesAndResolvesTypedServices(t *testing.T) {
	container := NewContainer()
	key := NewServiceKey[namedService]("test.named")

	if err := Provide(container, key, func(*Container) (namedService, error) {
		return namedServiceValue{name: "Bridra"}, nil
	}); err != nil {
		t.Fatalf("provide: %v", err)
	}

	service, err := Resolve(container, key)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if service.Name() != "Bridra" {
		t.Fatalf("name = %q", service.Name())
	}
	if !HasService(container, key) {
		t.Fatal("service should be registered")
	}
}

func TestContainerRejectsDuplicateAndMissingServices(t *testing.T) {
	container := NewContainer()
	key := NewServiceKey[string]("test.value")
	if err := Instance(container, key, "first"); err != nil {
		t.Fatalf("instance: %v", err)
	}
	if err := Instance(container, key, "second"); !errors.Is(err, ErrServiceAlreadyRegistered) {
		t.Fatalf("duplicate error = %v", err)
	}

	missing := NewServiceKey[int]("test.missing")
	if _, err := Resolve(container, missing); !errors.Is(err, ErrServiceNotFound) {
		t.Fatalf("missing error = %v", err)
	}
}

func TestContainerDoesNotRegisterFailedFactories(t *testing.T) {
	container := NewContainer()
	key := NewServiceKey[string]("test.failed")
	factoryError := errors.New("factory failed")

	err := Provide(container, key, func(*Container) (string, error) {
		return "", factoryError
	})

	if !errors.Is(err, factoryError) {
		t.Fatalf("error = %v", err)
	}
	if HasService(container, key) {
		t.Fatal("failed service was registered")
	}
}

func TestServiceKeysWithTheSameNameRemainTypeSafe(t *testing.T) {
	container := NewContainer()
	stringKey := NewServiceKey[string]("shared")
	intKey := NewServiceKey[int]("shared")

	if err := Instance(container, stringKey, "value"); err != nil {
		t.Fatalf("string instance: %v", err)
	}
	if err := Instance(container, intKey, 42); err != nil {
		t.Fatalf("int instance: %v", err)
	}

	if value, _ := Resolve(container, stringKey); value != "value" {
		t.Fatalf("string value = %q", value)
	}
	if value, _ := Resolve(container, intKey); value != 42 {
		t.Fatalf("int value = %d", value)
	}
}

func TestContainerFactoriesResolveRegisteredDependencies(t *testing.T) {
	container := NewContainer()
	baseKey := NewServiceKey[string]("base")
	lengthKey := NewServiceKey[int]("length")

	if err := Instance(container, baseKey, "Bridra"); err != nil {
		t.Fatalf("base instance: %v", err)
	}
	if err := Provide(container, lengthKey, func(container *Container) (int, error) {
		base, err := Resolve(container, baseKey)
		return len(base), err
	}); err != nil {
		t.Fatalf("provide dependent: %v", err)
	}
	if length, err := Resolve(container, lengthKey); err != nil || length != 6 {
		t.Fatalf("length = %d, error = %v", length, err)
	}
}
