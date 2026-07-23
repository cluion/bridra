package framework_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

type publicRepository interface {
	ID() int32
}

type publicRepositoryValue struct {
	id int32
}

func (repository *publicRepositoryValue) ID() int32 {
	return repository.id
}

func TestPublicContainerBindingsComposeThroughResolver(t *testing.T) {
	container := framework.NewContainer()
	concreteKey := framework.NewServiceKey[*publicRepositoryValue]("public.repository.concrete")
	repositoryKey := framework.NewServiceKey[publicRepository]("public.repository")
	consumerKey := framework.NewServiceKey[*publicRepositoryValue]("public.consumer")
	var builds atomic.Int32

	if err := framework.BindSingleton(
		container,
		concreteKey,
		func(framework.Resolver) (*publicRepositoryValue, error) {
			return &publicRepositoryValue{id: builds.Add(1)}, nil
		},
	); err != nil {
		t.Fatalf("bind singleton: %v", err)
	}
	if err := framework.Alias(container, repositoryKey, concreteKey); err != nil {
		t.Fatalf("alias: %v", err)
	}
	if err := framework.BindTransient(
		container,
		consumerKey,
		func(resolver framework.Resolver) (*publicRepositoryValue, error) {
			repository, err := framework.Resolve(resolver, repositoryKey)
			if err != nil {
				return nil, err
			}
			return &publicRepositoryValue{id: repository.ID()}, nil
		},
	); err != nil {
		t.Fatalf("bind transient: %v", err)
	}

	first, err := framework.Resolve(container, consumerKey)
	if err != nil {
		t.Fatalf("resolve first: %v", err)
	}
	second, err := framework.Resolve(container, consumerKey)
	if err != nil {
		t.Fatalf("resolve second: %v", err)
	}
	if first == second || first.ID() != 1 || second.ID() != 1 || builds.Load() != 1 {
		t.Fatalf("consumers = %#v, %#v; singleton builds = %d", first, second, builds.Load())
	}
}

func TestPublicRouterCreatesOneScopePerDispatch(t *testing.T) {
	application := framework.NewApplication(nil)
	container := application.Container()
	requestKey := framework.NewServiceKey[*publicRepositoryValue]("public.request")
	var builds atomic.Int32
	if err := framework.BindScoped(
		container,
		requestKey,
		func(framework.Resolver) (*publicRepositoryValue, error) {
			return &publicRepositoryValue{id: builds.Add(1)}, nil
		},
	); err != nil {
		t.Fatalf("bind scoped: %v", err)
	}

	router := application.Router()
	router.Handle("public.scope", func(ctx *framework.Context) (any, error) {
		first, err := framework.Resolve(ctx.Scope(), requestKey)
		if err != nil {
			return nil, err
		}
		second, err := framework.Resolve(ctx.Scope(), requestKey)
		if err != nil {
			return nil, err
		}
		if first != second {
			t.Fatal("request scope did not reuse its service")
		}
		return first.ID(), nil
	})

	first := router.Dispatch(context.Background(), framework.Request{ID: "1", Method: "public.scope"})
	second := router.Dispatch(context.Background(), framework.Request{ID: "2", Method: "public.scope"})
	if first.Error != nil || second.Error != nil {
		t.Fatalf("responses = %#v, %#v", first, second)
	}
	if first.Result != int32(1) || second.Result != int32(2) || builds.Load() != 2 {
		t.Fatalf("results = %#v, %#v; builds = %d", first.Result, second.Result, builds.Load())
	}
}

func TestPublicContextCanUseAnExplicitScope(t *testing.T) {
	container := framework.NewContainer()
	scope := container.NewScope()
	ctx := framework.NewContextWithScope(
		context.Background(),
		framework.Request{ID: "1", Method: "public.explicit-scope"},
		scope,
	)

	if ctx.Scope() != scope || ctx.Scope().Container() != container {
		t.Fatal("context did not retain the explicit scope")
	}
}
