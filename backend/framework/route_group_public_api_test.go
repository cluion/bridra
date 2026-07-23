package framework_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

func TestPublicRouteGroupAPI(t *testing.T) {
	router := framework.NewRouter()
	traceMiddleware := func(name string) framework.Middleware {
		return func(next framework.Handler) framework.Handler {
			return func(ctx *framework.Context) (any, error) {
				ctx.Trace = append(ctx.Trace, name+":before")
				result, err := next(ctx)
				ctx.Trace = append(ctx.Trace, name+":after")
				return result, err
			}
		}
	}
	if err := router.RegisterMiddlewareGroup("api", traceMiddleware("api")); err != nil {
		t.Fatalf("register middleware group: %v", err)
	}
	group, err := router.Group("reports", "api")
	if err != nil {
		t.Fatalf("route group: %v", err)
	}
	group.HandleWithPolicies(
		"read",
		func(ctx *framework.Context) (any, error) {
			ctx.Trace = append(ctx.Trace, "controller")
			return "report", nil
		},
		func(ctx *framework.Context) error {
			ctx.Trace = append(ctx.Trace, "policy")
			return nil
		},
	)

	response := router.Dispatch(context.Background(), framework.Request{
		ID: "1", Method: "reports.read",
	})
	want := []string{"api:before", "policy", "controller", "api:after"}
	if response.Error != nil || response.Result != "report" {
		t.Fatalf("response = %#v", response)
	}
	if !reflect.DeepEqual(response.Meta["pipeline"], want) {
		t.Fatalf("pipeline = %#v, want %#v", response.Meta["pipeline"], want)
	}
}
