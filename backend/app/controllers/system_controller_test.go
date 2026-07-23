package controllers

import (
	"testing"

	"github.com/cluion/bridra/backend/app/responses"
	"github.com/cluion/bridra/backend/framework"
)

func TestSystemHealthReportsProtocolVersion(t *testing.T) {
	result, err := NewSystemController().Health(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	health := result.(responses.HealthResponse)
	if health.Status != "ok" {
		t.Fatalf("status = %#v", health.Status)
	}
	if health.FrameworkVersion != framework.FrameworkVersion {
		t.Fatalf("frameworkVersion = %#v", health.FrameworkVersion)
	}
	if health.ProtocolVersion != framework.ProtocolVersion {
		t.Fatalf("protocolVersion = %#v", health.ProtocolVersion)
	}
}
