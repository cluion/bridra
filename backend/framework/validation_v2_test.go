package framework

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

type validationAddress struct {
	City string
}

var validationAddressRules = NewRuleRegistry[validationAddress](
	ForField(
		"city",
		func(address validationAddress) string { return strings.TrimSpace(address.City) },
		MaxLength(4, "City must be 4 characters or fewer."),
	),
)

func (address validationAddress) Validate() error {
	return validationAddressRules.Validate(address)
}

type validationWindow struct {
	Mode    *string
	Start   int
	End     int
	Address *validationAddress
}

func TestRuleRegistryCombinesFieldCrossFieldAndNestedViolations(t *testing.T) {
	registry := NewRuleRegistry[validationWindow](
		ForField(
			"mode",
			func(window validationWindow) *string { return window.Mode },
			Optional(OneOf("Mode must be draft or live.", "draft", "live")),
		),
		RuleFunc[validationWindow](func(window validationWindow) error {
			if window.End >= window.Start {
				return nil
			}
			return NewValidationErrors(FieldViolation{
				Field:   "end",
				Rule:    "after_or_equal",
				Message: "End must be after or equal to start.",
				Parameters: map[string]any{
					"other": "start",
				},
			})
		}),
		OptionalNestedField(
			"address",
			func(window validationWindow) *validationAddress { return window.Address },
		),
	)
	invalidMode := "archived"

	err := registry.Validate(validationWindow{
		Mode:    &invalidMode,
		Start:   10,
		End:     5,
		Address: &validationAddress{City: "Taipei"},
	})

	var validationErrors *ValidationErrors
	if !errors.As(err, &validationErrors) {
		t.Fatalf("error = %v, want ValidationErrors", err)
	}
	got := validationErrors.Violations
	if len(got) != 3 {
		t.Fatalf("violations = %#v", got)
	}
	fields := []string{got[0].Field, got[1].Field, got[2].Field}
	if !reflect.DeepEqual(fields, []string{"mode", "end", "address.city"}) {
		t.Fatalf("fields = %#v", fields)
	}
	if got[0].Rule != "one_of" || got[2].Rule != "max_length" {
		t.Fatalf("violations = %#v", got)
	}
}

func TestOptionalRulesIgnoreNilValues(t *testing.T) {
	registry := NewRuleRegistry[validationWindow](
		ForField(
			"mode",
			func(window validationWindow) *string { return window.Mode },
			Optional(OneOf("Mode is invalid.", "draft", "live")),
		),
		OptionalNestedField(
			"address",
			func(window validationWindow) *validationAddress { return window.Address },
		),
	)

	if err := registry.Validate(validationWindow{}); err != nil {
		t.Fatalf("validate optional values: %v", err)
	}
}

func TestNumericBoundsReportStableViolations(t *testing.T) {
	registry := NewRuleRegistry[validationWindow](
		ForField(
			"start",
			func(window validationWindow) int { return window.Start },
			Minimum(1, "Start must be at least 1."),
		),
		ForField(
			"end",
			func(window validationWindow) int { return window.End },
			Maximum(10, "End must be 10 or less."),
		),
	)

	err := registry.Validate(validationWindow{Start: 0, End: 11})
	var validationErrors *ValidationErrors
	if !errors.As(err, &validationErrors) {
		t.Fatalf("error = %v, want ValidationErrors", err)
	}
	violations := validationErrors.Violations
	if len(violations) != 2 || violations[0].Rule != "minimum" ||
		violations[1].Rule != "maximum" {
		t.Fatalf("violations = %#v", violations)
	}
	if violations[0].Parameters["min"] != 1 ||
		violations[1].Parameters["max"] != 10 {
		t.Fatalf("violations = %#v, want numeric parameters", violations)
	}
}

func TestRuleRegistryRejectsTypedNilRuleWithoutMutation(t *testing.T) {
	registry := NewRuleRegistry[validationWindow]()
	var invalid RuleFunc[validationWindow]

	if err := registry.Register(invalid); !errors.Is(err, ErrInvalidValidationRule) {
		t.Fatalf("error = %v, want ErrInvalidValidationRule", err)
	}
	if err := registry.Validate(validationWindow{}); err != nil {
		t.Fatalf("registry mutated after failed registration: %v", err)
	}
}

func TestRuleRegistryPreservesNonValidationErrors(t *testing.T) {
	want := errors.New("dependency unavailable")
	registry := NewRuleRegistry[validationWindow](
		RuleFunc[validationWindow](func(validationWindow) error { return want }),
	)

	if err := registry.Validate(validationWindow{}); !errors.Is(err, want) {
		t.Fatalf("error = %v, want original error", err)
	}
}
