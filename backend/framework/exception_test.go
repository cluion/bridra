package framework

import (
	"context"
	"errors"
	"testing"
)

type publicDomainError struct {
	resource string
}

func (err *publicDomainError) Error() string {
	return "missing " + err.resource
}

func TestRouterUsesTypedExceptionMapper(t *testing.T) {
	renderer := NewExceptionRegistry(MapException(func(err *publicDomainError) *RPCError {
		return NewErrorWithData(
			"resource_missing",
			"The requested resource does not exist.",
			map[string]any{"resource": err.resource},
		)
	}))
	router := NewRouter()
	if err := router.SetExceptionRenderer(renderer); err != nil {
		t.Fatalf("set exception renderer: %v", err)
	}
	router.Handle("test.exception", func(*Context) (any, error) {
		return nil, &publicDomainError{resource: "profile"}
	})

	response := router.Dispatch(context.Background(), Request{ID: "1", Method: "test.exception"})

	if response.Error == nil || response.Error.Code != "resource_missing" {
		t.Fatalf("error = %#v", response.Error)
	}
	if response.Error.Data["resource"] != "profile" {
		t.Fatalf("data = %#v", response.Error.Data)
	}
}

func TestExceptionRegistryFallsBackToFrameworkRendering(t *testing.T) {
	renderer := NewExceptionRegistry()

	validation := renderer.Render(NewValidationErrors(FieldViolation{Field: "name"}))
	if validation.Code != "validation_error" {
		t.Fatalf("validation code = %q", validation.Code)
	}
	internal := renderer.Render(errors.New("database secret"))
	if internal.Code != "internal_error" || internal.Message == "database secret" {
		t.Fatalf("internal error = %#v", internal)
	}
}

func TestRouterRejectsTypedNilExceptionRenderer(t *testing.T) {
	router := NewRouter()
	var renderer ExceptionRendererFunc

	if err := router.SetExceptionRenderer(renderer); !errors.Is(err, ErrInvalidExceptionRenderer) {
		t.Fatalf("error = %v, want ErrInvalidExceptionRenderer", err)
	}
}

func TestRouterFallsBackWhenCustomRendererReturnsNil(t *testing.T) {
	router := NewRouter()
	if err := router.SetExceptionRenderer(ExceptionRendererFunc(func(error) *RPCError {
		return nil
	})); err != nil {
		t.Fatalf("set exception renderer: %v", err)
	}
	router.Handle("test.nil-renderer", func(*Context) (any, error) {
		return nil, errors.New("hidden")
	})

	response := router.Dispatch(context.Background(), Request{ID: "1", Method: "test.nil-renderer"})
	if response.Error == nil || response.Error.Code != "internal_error" {
		t.Fatalf("error = %#v", response.Error)
	}
}
