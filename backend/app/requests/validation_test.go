package requests

import (
	"errors"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

func TestHelloRequestValidatesEnumAndNestedObject(t *testing.T) {
	invalidTone := "robotic"
	request := HelloRequest{
		Name: "Codex",
		Tone: &invalidTone,
		Profile: &GreetingProfileRequest{
			Nickname: "a nickname that is definitely too long",
		},
	}

	err := request.Validate()
	var validationErrors *framework.ValidationErrors
	if !errors.As(err, &validationErrors) {
		t.Fatalf("error = %v, want ValidationErrors", err)
	}
	if len(validationErrors.Violations) != 2 {
		t.Fatalf("violations = %#v", validationErrors.Violations)
	}
	if validationErrors.Violations[0].Field != "tone" ||
		validationErrors.Violations[0].Rule != "one_of" {
		t.Fatalf("enum violation = %#v", validationErrors.Violations[0])
	}
	if validationErrors.Violations[1].Field != "profile.nickname" {
		t.Fatalf("nested violation = %#v", validationErrors.Violations[1])
	}
}

func TestHelloRequestAllowsMissingNullableFields(t *testing.T) {
	if err := (HelloRequest{Name: "Codex"}).Validate(); err != nil {
		t.Fatalf("validate nullable fields: %v", err)
	}
}
