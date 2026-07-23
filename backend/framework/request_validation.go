package framework

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"
)

// RequestPayloadValidator validates wire-level properties which cannot be
// recovered from a decoded Go value, such as whether a field was omitted or
// explicitly set to null.
type RequestPayloadValidator interface {
	ValidatePayload([]byte) error
}

// RequestNormalizer prepares a decoded request before semantic validation and
// before the value is returned to a Controller.
type RequestNormalizer interface {
	Normalize()
}

// ValidateRequestPayload enforces the JSON presence contract represented by a
// request DTO. Exported fields without `omitempty` are required and non-null.
// Fields marked `omitempty` are optional and may explicitly be null.
func ValidateRequestPayload[T any](payload []byte) error {
	valueType := reflect.TypeFor[T]()
	for valueType.Kind() == reflect.Pointer {
		valueType = valueType.Elem()
	}
	if valueType.Kind() != reflect.Struct {
		return nil
	}

	if len(bytes.TrimSpace(payload)) == 0 {
		payload = []byte("{}")
	}
	violations, err := validateJSONStruct(valueType, payload, "")
	if err != nil {
		return NewError("invalid_params", "The request parameters are invalid.")
	}
	if len(violations) == 0 {
		return nil
	}
	return NewValidationErrors(violations...)
}

func validateJSONStruct(
	valueType reflect.Type,
	payload []byte,
	prefix string,
) ([]FieldViolation, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return nil, errors.New("request payload must be an object")
	}

	violations := make([]FieldViolation, 0)
	for index := 0; index < valueType.NumField(); index++ {
		field := valueType.Field(index)
		if !field.IsExported() {
			continue
		}
		name, omitEmpty, skip := requestJSONField(field)
		if skip {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		raw, exists := lookupJSONField(object, name)
		if !exists {
			if !omitEmpty {
				violations = append(violations, FieldViolation{
					Field:   path,
					Rule:    "required",
					Message: requestFieldLabel(name) + " is required.",
				})
			}
			continue
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			if !omitEmpty {
				violations = append(violations, FieldViolation{
					Field:   path,
					Rule:    "not_null",
					Message: requestFieldLabel(name) + " must not be null.",
				})
			}
			continue
		}

		nestedType := field.Type
		for nestedType.Kind() == reflect.Pointer {
			nestedType = nestedType.Elem()
		}
		if nestedType.Kind() != reflect.Struct {
			continue
		}
		nested, err := validateJSONStruct(nestedType, raw, path)
		if err != nil {
			return nil, err
		}
		violations = append(violations, nested...)
	}
	return violations, nil
}

func requestJSONField(field reflect.StructField) (name string, omitEmpty bool, skip bool) {
	tag := field.Tag.Get("json")
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "-" {
		return "", false, true
	}
	if name == "" {
		name = field.Name
	}
	for _, option := range parts[1:] {
		if option == "omitempty" {
			omitEmpty = true
		}
	}
	return name, omitEmpty, false
}

func lookupJSONField(object map[string]json.RawMessage, name string) (json.RawMessage, bool) {
	if value, exists := object[name]; exists {
		return value, true
	}
	for candidate, value := range object {
		if strings.EqualFold(candidate, name) {
			return value, true
		}
	}
	return nil, false
}

func requestFieldLabel(name string) string {
	first, size := utf8.DecodeRuneInString(name)
	if first == utf8.RuneError && size == 0 {
		return name
	}
	return string(unicode.ToUpper(first)) + name[size:]
}
