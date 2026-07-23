package framework

import (
	"context"
	"testing"
)

type validatedRequest struct {
	Name string `json:"name"`
}

func (request *validatedRequest) Validate() error {
	if request.Name == "" {
		return NewValidationErrors(FieldViolation{
			Field:   "name",
			Rule:    "required",
			Message: "Name is required.",
		})
	}
	return nil
}

func TestBindAndValidateSupportsPointerValidators(t *testing.T) {
	ctx := NewContext(context.Background(), Request{Params: []byte(`{"name":"Codex"}`)})

	request, err := BindAndValidate[validatedRequest](ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if request.Name != "Codex" {
		t.Fatalf("name = %q", request.Name)
	}
}

func TestValidationErrorsBecomeStructuredRPCErrors(t *testing.T) {
	ctx := NewContext(context.Background(), Request{Params: []byte(`{"name":""}`)})

	_, err := BindAndValidate[validatedRequest](ctx)
	rpcError := AsRPCError(err)
	if rpcError.Code != "validation_error" {
		t.Fatalf("code = %q", rpcError.Code)
	}
	violations, ok := rpcError.Data["violations"].([]FieldViolation)
	if !ok || len(violations) != 1 {
		t.Fatalf("violations = %#v", rpcError.Data["violations"])
	}
	if violations[0].Field != "name" || violations[0].Rule != "required" {
		t.Fatalf("violation = %#v", violations[0])
	}
}
