package framework

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"unicode/utf8"
)

var ErrInvalidValidationRule = errors.New("framework: validation rule cannot be nil")

type Validatable interface {
	Validate() error
}

type FieldViolation struct {
	Field      string         `json:"field"`
	Rule       string         `json:"rule"`
	Message    string         `json:"message"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

type ValidationErrors struct {
	Violations []FieldViolation
}

func NewValidationErrors(violations ...FieldViolation) *ValidationErrors {
	return &ValidationErrors{Violations: append([]FieldViolation(nil), violations...)}
}

func (e *ValidationErrors) Error() string {
	if e == nil || len(e.Violations) == 0 {
		return "request validation failed"
	}
	first := e.Violations[0]
	return fmt.Sprintf("request validation failed for %s: %s", first.Field, first.Message)
}

func (e *ValidationErrors) WithPrefix(prefix string) *ValidationErrors {
	if e == nil {
		return NewValidationErrors()
	}
	violations := make([]FieldViolation, 0, len(e.Violations))
	for _, violation := range e.Violations {
		violation.Parameters = cloneValidationParameters(violation.Parameters)
		if violation.Field == "" {
			violation.Field = prefix
		} else if prefix != "" {
			violation.Field = prefix + "." + violation.Field
		}
		violations = append(violations, violation)
	}
	return NewValidationErrors(violations...)
}

type Rule[T any] interface {
	Validate(T) error
}

type RuleFunc[T any] func(T) error

func (rule RuleFunc[T]) Validate(value T) error {
	return rule(value)
}

type ValueViolation struct {
	Rule       string
	Message    string
	Parameters map[string]any
}

type ValueRule[T any] interface {
	ValidateValue(T) *ValueViolation
}

type ValueRuleFunc[T any] func(T) *ValueViolation

func (rule ValueRuleFunc[T]) ValidateValue(value T) *ValueViolation {
	return rule(value)
}

type RuleRegistry[T any] struct {
	mu    sync.RWMutex
	rules []Rule[T]
}

func NewRuleRegistry[T any](rules ...Rule[T]) *RuleRegistry[T] {
	registry := &RuleRegistry[T]{}
	if err := registry.Register(rules...); err != nil {
		panic(err)
	}
	return registry
}

func (registry *RuleRegistry[T]) Register(rules ...Rule[T]) error {
	for _, rule := range rules {
		if validationRuleIsNil(rule) {
			return ErrInvalidValidationRule
		}
	}
	registry.mu.Lock()
	registry.rules = append(registry.rules, rules...)
	registry.mu.Unlock()
	return nil
}

func (registry *RuleRegistry[T]) Validate(value T) error {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	rules := append([]Rule[T](nil), registry.rules...)
	registry.mu.RUnlock()

	violations := make([]FieldViolation, 0)
	for _, rule := range rules {
		err := rule.Validate(value)
		if err == nil {
			continue
		}
		var validationErrors *ValidationErrors
		if !errors.As(err, &validationErrors) {
			return err
		}
		violations = append(violations, validationErrors.Violations...)
	}
	if len(violations) == 0 {
		return nil
	}
	return NewValidationErrors(violations...)
}

func ForField[T, V any](
	field string,
	value func(T) V,
	rules ...ValueRule[V],
) Rule[T] {
	if value == nil {
		panic(ErrInvalidValidationRule)
	}
	for _, rule := range rules {
		if valueRuleIsNil(rule) {
			panic(ErrInvalidValidationRule)
		}
	}
	return RuleFunc[T](func(input T) error {
		fieldValue := value(input)
		violations := make([]FieldViolation, 0, len(rules))
		for _, rule := range rules {
			violation := rule.ValidateValue(fieldValue)
			if violation == nil {
				continue
			}
			violations = append(violations, FieldViolation{
				Field:      field,
				Rule:       violation.Rule,
				Message:    violation.Message,
				Parameters: cloneValidationParameters(violation.Parameters),
			})
		}
		if len(violations) == 0 {
			return nil
		}
		return NewValidationErrors(violations...)
	})
}

func MaxLength(maximum int, message string) ValueRule[string] {
	if maximum < 0 {
		panic("framework: maximum length cannot be negative")
	}
	return ValueRuleFunc[string](func(value string) *ValueViolation {
		if utf8.RuneCountInString(value) <= maximum {
			return nil
		}
		return &ValueViolation{
			Rule:       "max_length",
			Message:    message,
			Parameters: map[string]any{"max": maximum},
		}
	})
}

func MinLength(minimum int, message string) ValueRule[string] {
	if minimum < 0 {
		panic("framework: minimum length cannot be negative")
	}
	return ValueRuleFunc[string](func(value string) *ValueViolation {
		if utf8.RuneCountInString(value) >= minimum {
			return nil
		}
		return &ValueViolation{
			Rule:       "min_length",
			Message:    message,
			Parameters: map[string]any{"min": minimum},
		}
	})
}

func OneOf[T comparable](message string, allowed ...T) ValueRule[T] {
	values := append([]T(nil), allowed...)
	return ValueRuleFunc[T](func(value T) *ValueViolation {
		for _, candidate := range values {
			if value == candidate {
				return nil
			}
		}
		parameters := make([]T, len(values))
		copy(parameters, values)
		return &ValueViolation{
			Rule:       "one_of",
			Message:    message,
			Parameters: map[string]any{"values": parameters},
		}
	})
}

func Optional[T any](rule ValueRule[T]) ValueRule[*T] {
	if valueRuleIsNil(rule) {
		panic(ErrInvalidValidationRule)
	}
	return ValueRuleFunc[*T](func(value *T) *ValueViolation {
		if value == nil {
			return nil
		}
		return rule.ValidateValue(*value)
	})
}

func NestedField[T any, V Validatable](field string, value func(T) V) Rule[T] {
	if value == nil {
		panic(ErrInvalidValidationRule)
	}
	return RuleFunc[T](func(input T) error {
		return validateNested(field, value(input))
	})
}

func OptionalNestedField[T any, V Validatable](field string, value func(T) V) Rule[T] {
	if value == nil {
		panic(ErrInvalidValidationRule)
	}
	return RuleFunc[T](func(input T) error {
		nested := value(input)
		if interfaceIsNil(nested) {
			return nil
		}
		return validateNested(field, nested)
	})
}

func validateNested(field string, validator Validatable) error {
	err := validator.Validate()
	if err == nil {
		return nil
	}
	var validationErrors *ValidationErrors
	if !errors.As(err, &validationErrors) {
		return err
	}
	return validationErrors.WithPrefix(field)
}

func validationRuleIsNil[T any](rule Rule[T]) bool {
	return interfaceIsNil(rule)
}

func valueRuleIsNil[T any](rule ValueRule[T]) bool {
	return interfaceIsNil(rule)
}

func interfaceIsNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func cloneValidationParameters(parameters map[string]any) map[string]any {
	if parameters == nil {
		return nil
	}
	cloned := make(map[string]any, len(parameters))
	for key, value := range parameters {
		cloned[key] = value
	}
	return cloned
}

func BindAndValidate[T any](ctx *Context) (T, error) {
	value, err := BindParams[T](ctx)
	if err != nil {
		return value, err
	}
	if validator, ok := any(value).(RequestPayloadValidator); ok {
		if err := validator.ValidatePayload(ctx.Request.Params); err != nil {
			return value, err
		}
	} else if validator, ok := any(&value).(RequestPayloadValidator); ok {
		if err := validator.ValidatePayload(ctx.Request.Params); err != nil {
			return value, err
		}
	}
	if normalizer, ok := any(&value).(RequestNormalizer); ok {
		normalizer.Normalize()
	} else if normalizer, ok := any(value).(RequestNormalizer); ok {
		normalizer.Normalize()
	}

	if validator, ok := any(value).(Validatable); ok {
		if err := validator.Validate(); err != nil {
			return value, err
		}
		return value, nil
	}
	if validator, ok := any(&value).(Validatable); ok {
		if err := validator.Validate(); err != nil {
			return value, err
		}
	}
	return value, nil
}
