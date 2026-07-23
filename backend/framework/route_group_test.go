package framework

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func passThrough(next Handler) Handler {
	return next
}

func TestNamedMiddlewareGroupCanBeAppliedGlobally(t *testing.T) {
	router := NewRouter()
	if err := router.RegisterMiddlewareGroup(
		"rpc",
		Traced("first", passThrough),
		Traced("second", passThrough),
	); err != nil {
		t.Fatalf("register middleware group: %v", err)
	}
	if err := router.UseMiddlewareGroups("rpc"); err != nil {
		t.Fatalf("use middleware group: %v", err)
	}
	router.Handle("test", func(ctx *Context) (any, error) {
		ctx.Trace = append(ctx.Trace, "controller")
		return "ok", nil
	})

	response := router.Dispatch(context.Background(), Request{ID: "1", Method: "test"})
	want := []string{
		"first:before",
		"second:before",
		"controller",
		"second:after",
		"first:after",
	}
	if response.Error != nil || !reflect.DeepEqual(response.Meta["pipeline"], want) {
		t.Fatalf("response = %#v, want pipeline %#v", response, want)
	}
}

func TestNestedRouteGroupsComposePrefixMiddlewareAndPolicies(t *testing.T) {
	router := NewRouter()
	if err := router.RegisterMiddlewareGroup(
		"tenant",
		Traced("tenant", passThrough),
	); err != nil {
		t.Fatalf("register middleware group: %v", err)
	}
	api, err := router.Group("api", "tenant")
	if err != nil {
		t.Fatalf("api group: %v", err)
	}
	api.Use(Traced("inline", passThrough))
	api.UsePolicies(func(ctx *Context) error {
		ctx.Trace = append(ctx.Trace, "group-policy")
		return nil
	})
	v1, err := api.Group("v1")
	if err != nil {
		t.Fatalf("v1 group: %v", err)
	}
	v1.HandleWithPolicies(
		"users.list",
		func(ctx *Context) (any, error) {
			ctx.Trace = append(ctx.Trace, "controller")
			return "users", nil
		},
		func(ctx *Context) error {
			ctx.Trace = append(ctx.Trace, "method-policy")
			return nil
		},
	)

	response := router.Dispatch(context.Background(), Request{
		ID: "1", Method: "api.v1.users.list",
	})
	want := []string{
		"tenant:before",
		"inline:before",
		"group-policy",
		"method-policy",
		"controller",
		"inline:after",
		"tenant:after",
	}
	if response.Error != nil || response.Result != "users" {
		t.Fatalf("response = %#v", response)
	}
	if !reflect.DeepEqual(response.Meta["pipeline"], want) {
		t.Fatalf("pipeline = %#v, want %#v", response.Meta["pipeline"], want)
	}
}

func TestMethodPolicyRejectsBeforeController(t *testing.T) {
	router := NewRouter()
	controllerCalled := false
	router.HandleWithPolicies(
		"admin.read",
		func(*Context) (any, error) {
			controllerCalled = true
			return "secret", nil
		},
		func(*Context) error {
			return NewError("forbidden", "The method policy denied this request.")
		},
	)

	response := router.Dispatch(context.Background(), Request{ID: "1", Method: "admin.read"})
	if response.Error == nil || response.Error.Code != "forbidden" {
		t.Fatalf("response = %#v", response)
	}
	if controllerCalled {
		t.Fatal("controller ran after method policy rejection")
	}
}

func TestMiddlewareGroupRegistrationErrorsAreStable(t *testing.T) {
	router := NewRouter()
	if err := router.RegisterMiddlewareGroup("", passThrough); !errors.Is(err, ErrInvalidMiddlewareGroup) {
		t.Fatalf("empty name error = %v", err)
	}
	if err := router.RegisterMiddlewareGroup("invalid", nil); !errors.Is(err, ErrInvalidMiddlewareGroup) {
		t.Fatalf("nil middleware error = %v", err)
	}
	if err := router.RegisterMiddlewareGroup("rpc", passThrough); err != nil {
		t.Fatalf("register middleware group: %v", err)
	}
	if err := router.RegisterMiddlewareGroup("rpc", passThrough); !errors.Is(err, ErrMiddlewareGroupAlreadyDefined) {
		t.Fatalf("duplicate error = %v", err)
	}
	if err := router.UseMiddlewareGroups("missing"); !errors.Is(err, ErrMiddlewareGroupNotFound) {
		t.Fatalf("missing error = %v", err)
	}
	if _, err := router.Group("invalid..prefix"); !errors.Is(err, ErrInvalidRouteGroup) {
		t.Fatalf("route group error = %v", err)
	}
}

func TestUnknownMiddlewareGroupDoesNotPartiallyMutateRouter(t *testing.T) {
	router := NewRouter()
	if err := router.RegisterMiddlewareGroup("known", Traced("known", passThrough)); err != nil {
		t.Fatalf("register middleware group: %v", err)
	}
	if err := router.UseMiddlewareGroups("known", "missing"); !errors.Is(err, ErrMiddlewareGroupNotFound) {
		t.Fatalf("use error = %v", err)
	}
	router.Handle("test", func(*Context) (any, error) { return "ok", nil })

	response := router.Dispatch(context.Background(), Request{ID: "1", Method: "test"})
	if pipeline := response.Meta["pipeline"]; !reflect.DeepEqual(pipeline, []string(nil)) {
		t.Fatalf("pipeline = %#v, want no partial middleware", pipeline)
	}
}
