package mobilebridge

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

func TestRuntimeDispatchesApplicationRPCWithoutHTTP(t *testing.T) {
	runtime, err := NewRuntime("embedded-test-token")
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() {
		_ = runtime.Close(5000)
	})

	responseJSON, err := runtime.CallJSON(
		`{"id":"health-1","method":"system.health","meta":{"token":"embedded-test-token"}}`,
	)
	if err != nil {
		t.Fatalf("CallJSON() error = %v", err)
	}
	var response struct {
		ID     string `json:"id"`
		Result struct {
			Status  string `json:"status"`
			Runtime string `json:"runtime"`
		} `json:"result"`
		Error *framework.RPCError `json:"error"`
	}
	if err := json.Unmarshal([]byte(responseJSON), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != "health-1" || response.Error != nil ||
		response.Result.Status != "ok" || response.Result.Runtime != embeddedRuntimeName {
		t.Fatalf("response = %#v", response)
	}
}

func TestRuntimePreservesRPCAuthentication(t *testing.T) {
	runtime, err := NewRuntime("embedded-test-token")
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() {
		_ = runtime.Close(5000)
	})

	responseJSON, err := runtime.CallJSON(
		`{"id":"health-1","method":"system.health","meta":{"token":"wrong"}}`,
	)
	if err != nil {
		t.Fatalf("CallJSON() error = %v", err)
	}
	var response framework.Response
	if err := json.Unmarshal([]byte(responseJSON), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error == nil || response.Error.Code != "unauthorized" {
		t.Fatalf("response = %#v", response)
	}
}

func TestRuntimeValidatesCloseTimeout(t *testing.T) {
	runtime, err := NewRuntime("embedded-test-token")
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if err := runtime.Close(0); !errors.Is(err, ErrInvalidCloseTimeout) {
		t.Fatalf("Close() error = %v", err)
	}
	if err := runtime.Close(5000); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
