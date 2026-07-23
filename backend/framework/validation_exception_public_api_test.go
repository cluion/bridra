package framework_test

import (
	"context"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

type publicDateRange struct {
	Start int
	End   int
}

type publicConflict struct{}

func (*publicConflict) Error() string { return "conflict" }

func TestPublicValidationAndExceptionComposition(t *testing.T) {
	rules := framework.NewRuleRegistry[publicDateRange](
		framework.RuleFunc[publicDateRange](func(value publicDateRange) error {
			if value.End >= value.Start {
				return nil
			}
			return framework.NewValidationErrors(framework.FieldViolation{
				Field:   "end",
				Rule:    "after_or_equal",
				Message: "End must be after or equal to start.",
			})
		}),
	)
	if err := rules.Validate(publicDateRange{Start: 2, End: 1}); err == nil {
		t.Fatal("expected cross-field validation error")
	}

	router := framework.NewRouter()
	renderer := framework.NewExceptionRegistry(
		framework.MapException(func(*publicConflict) *framework.RPCError {
			return framework.NewError("conflict", "The operation conflicts with current state.")
		}),
	)
	if err := router.SetExceptionRenderer(renderer); err != nil {
		t.Fatalf("set renderer: %v", err)
	}
	router.Handle("public.conflict", func(*framework.Context) (any, error) {
		return nil, &publicConflict{}
	})

	response := router.Dispatch(context.Background(), framework.Request{
		ID: "1", Method: "public.conflict",
	})
	if response.Error == nil || response.Error.Code != "conflict" {
		t.Fatalf("response error = %#v", response.Error)
	}
}
