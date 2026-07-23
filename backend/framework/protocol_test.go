package framework

import (
	"context"
	"errors"
	"testing"
)

func TestBindParamsRejectsUnknownFields(t *testing.T) {
	ctx := NewContext(context.Background(), Request{
		Params: []byte(`{"name":"Codex","extra":true}`),
	})

	_, err := BindParams[struct {
		Name string `json:"name"`
	}](ctx)

	rpcError, ok := err.(*RPCError)
	if !ok || rpcError.Code != "invalid_params" {
		t.Fatalf("error = %#v, want invalid_params", err)
	}
}

func TestAsRPCErrorHidesInternalErrors(t *testing.T) {
	rpcError := AsRPCError(errors.New("database password leaked"))

	if rpcError.Code != "internal_error" {
		t.Fatalf("code = %q", rpcError.Code)
	}
	if rpcError.Message != "The Go backend could not complete the request." {
		t.Fatalf("message = %q", rpcError.Message)
	}
}

func TestNewErrorWithDataPreservesDetails(t *testing.T) {
	rpcError := NewErrorWithData("validation_error", "Invalid.", map[string]any{"field": "name"})

	if rpcError.Data["field"] != "name" {
		t.Fatalf("data = %#v", rpcError.Data)
	}
}
