package requests

import (
	"context"
	"errors"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

func TestHelloRequestEnforcesWirePresenceAndNullability(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		field   string
		rule    string
	}{
		{name: "missing name", payload: `{}`, field: "name", rule: "required"},
		{name: "null name", payload: `{"name":null}`, field: "name", rule: "not_null"},
		{name: "blank name", payload: `{"name":"   "}`, field: "name", rule: "min_length"},
		{
			name:    "missing nested nickname",
			payload: `{"name":"Codex","profile":{}}`,
			field:   "profile.nickname",
			rule:    "required",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := framework.NewContext(context.Background(), framework.Request{
				Params: []byte(test.payload),
			})
			_, err := framework.BindAndValidate[HelloRequest](ctx)
			var validationErrors *framework.ValidationErrors
			if !errors.As(err, &validationErrors) {
				t.Fatalf("error = %v, want ValidationErrors", err)
			}
			if len(validationErrors.Violations) != 1 {
				t.Fatalf("violations = %#v", validationErrors.Violations)
			}
			violation := validationErrors.Violations[0]
			if violation.Field != test.field || violation.Rule != test.rule {
				t.Fatalf("violation = %#v", violation)
			}
		})
	}
}

func TestHelloRequestNormalizesTrimmedFields(t *testing.T) {
	ctx := framework.NewContext(context.Background(), framework.Request{
		Params: []byte(`{"name":"  Codex  ","tone":null,"profile":{"nickname":"  C  "}}`),
	})

	request, err := framework.BindAndValidate[HelloRequest](ctx)
	if err != nil {
		t.Fatalf("bind and validate: %v", err)
	}
	if request.Name != "Codex" {
		t.Fatalf("name = %q", request.Name)
	}
	if request.Tone != nil {
		t.Fatalf("tone = %#v, want nil", request.Tone)
	}
	if request.Profile == nil || request.Profile.Nickname != "C" {
		t.Fatalf("profile = %#v", request.Profile)
	}
}

func TestHelloRequestAllowsExplicitNullForNullableFields(t *testing.T) {
	ctx := framework.NewContext(context.Background(), framework.Request{
		Params: []byte(`{"name":"Codex","tone":null,"profile":null}`),
	})

	if _, err := framework.BindAndValidate[HelloRequest](ctx); err != nil {
		t.Fatalf("bind nullable fields: %v", err)
	}
}
