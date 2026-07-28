package main

import (
	"context"
	"errors"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

func TestBuildApplicationLoadsTokenFromEnvironment(t *testing.T) {
	t.Setenv("BRIDRA_BACKEND_TOKEN", "environment-token")

	application, err := buildApplication("", false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Cleanup(func() {
		if err := shutdownApplication(application); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	response := application.Router().Dispatch(context.Background(), framework.Request{
		ID: "1", Method: "system.health", Meta: map[string]string{"token": "environment-token"},
	})
	if response.Error != nil {
		t.Fatalf("response error: %v", response.Error)
	}
}

func TestBuildApplicationExplicitTokenOverridesEnvironment(t *testing.T) {
	t.Setenv("BRIDRA_BACKEND_TOKEN", "environment-token")

	application, err := buildApplication("runtime-token", true)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Cleanup(func() {
		if err := shutdownApplication(application); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	response := application.Router().Dispatch(context.Background(), framework.Request{
		ID: "1", Method: "system.health", Meta: map[string]string{"token": "runtime-token"},
	})
	if response.Error != nil {
		t.Fatalf("response error: %v", response.Error)
	}
}

func TestBuildApplicationReportsMissingTokenAsConfigError(t *testing.T) {
	t.Setenv("BRIDRA_BACKEND_TOKEN", "")

	_, err := buildApplication("", false)
	var loadErrors *framework.ConfigLoadErrors
	if !errors.As(err, &loadErrors) {
		t.Fatalf("error = %v, want ConfigLoadErrors", err)
	}
}
