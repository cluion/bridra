package codegen

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
)

const SupportedSchemaVersion = 1

var (
	methodPattern     = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9]*)+$`)
	identifierPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)
)

type Schema struct {
	SchemaVersion   int      `json:"schemaVersion"`
	ProtocolVersion int      `json:"protocolVersion"`
	Methods         []Method `json:"methods"`
}

type Method struct {
	Name       string  `json:"name"`
	ClientName string  `json:"clientName"`
	Stream     bool    `json:"stream,omitempty"`
	Params     *Object `json:"params,omitempty"`
	Result     Object  `json:"result"`
	Meta       []Field `json:"meta,omitempty"`
}

type Object struct {
	GoType   string  `json:"goType"`
	DartType string  `json:"dartType"`
	Fields   []Field `json:"fields"`
}

type Field struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Format    string   `json:"format,omitempty"`
	Array     bool     `json:"array,omitempty"`
	Nullable  bool     `json:"nullable,omitempty"`
	Enum      []string `json:"enum,omitempty"`
	Object    *Object  `json:"object,omitempty"`
	MinLength int      `json:"minLength,omitempty"`
	MaxLength int      `json:"maxLength,omitempty"`
	Trim      bool     `json:"trim,omitempty"`
}

func LoadSchema(path string) (Schema, error) {
	file, err := os.Open(path)
	if err != nil {
		return Schema{}, fmt.Errorf("codegen: open schema: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var schema Schema
	if err := decoder.Decode(&schema); err != nil {
		return Schema{}, fmt.Errorf("codegen: decode schema: %w", err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return Schema{}, err
	}
	if err := schema.Validate(); err != nil {
		return Schema{}, err
	}
	return schema, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("codegen: schema contains multiple JSON values")
		}
		return fmt.Errorf("codegen: decode schema: %w", err)
	}
	return nil
}

func (schema Schema) Validate() error {
	if schema.SchemaVersion != SupportedSchemaVersion {
		return fmt.Errorf(
			"codegen: unsupported schema version %d; expected %d",
			schema.SchemaVersion,
			SupportedSchemaVersion,
		)
	}
	if schema.ProtocolVersion < 1 {
		return fmt.Errorf("codegen: protocolVersion must be positive")
	}
	if len(schema.Methods) == 0 {
		return fmt.Errorf("codegen: schema must define at least one method")
	}

	methods := make(map[string]struct{}, len(schema.Methods))
	clients := make(map[string]struct{}, len(schema.Methods))
	goTypes := make(map[string]string, len(schema.Methods)*2)
	dartTypes := make(map[string]string, len(schema.Methods)*2)
	for index, method := range schema.Methods {
		path := fmt.Sprintf("methods[%d]", index)
		if !methodPattern.MatchString(method.Name) {
			return fmt.Errorf("codegen: %s.name %q is invalid", path, method.Name)
		}
		if _, exists := methods[method.Name]; exists {
			return fmt.Errorf("codegen: duplicate method %q", method.Name)
		}
		methods[method.Name] = struct{}{}
		if !identifierPattern.MatchString(method.ClientName) {
			return fmt.Errorf("codegen: %s.clientName %q is invalid", path, method.ClientName)
		}
		if _, exists := clients[method.ClientName]; exists {
			return fmt.Errorf("codegen: duplicate client method %q", method.ClientName)
		}
		clients[method.ClientName] = struct{}{}

		if method.Params != nil {
			if err := validateObject(path+".params", *method.Params, goTypes, dartTypes, true); err != nil {
				return err
			}
		}
		if err := validateObject(path+".result", method.Result, goTypes, dartTypes, true); err != nil {
			return err
		}
		if err := validateFields(path+".meta", method.Meta, goTypes, dartTypes, true); err != nil {
			return err
		}
	}
	return nil
}

func validateObject(
	path string,
	object Object,
	goTypes map[string]string,
	dartTypes map[string]string,
	allowFile bool,
) error {
	if !identifierPattern.MatchString(object.GoType) {
		return fmt.Errorf("codegen: %s.goType %q is invalid", path, object.GoType)
	}
	if !identifierPattern.MatchString(object.DartType) {
		return fmt.Errorf("codegen: %s.dartType %q is invalid", path, object.DartType)
	}
	if previous, exists := goTypes[object.GoType]; exists {
		return fmt.Errorf(
			"codegen: generated Go type %q is shared by %s and %s",
			object.GoType,
			previous,
			path,
		)
	}
	goTypes[object.GoType] = path
	if previous, exists := dartTypes[object.DartType]; exists {
		return fmt.Errorf(
			"codegen: generated Dart type %q is shared by %s and %s",
			object.DartType,
			previous,
			path,
		)
	}
	dartTypes[object.DartType] = path
	if len(object.Fields) == 0 {
		return fmt.Errorf("codegen: %s.fields must not be empty", path)
	}
	return validateFields(path+".fields", object.Fields, goTypes, dartTypes, allowFile)
}

func validateFields(
	path string,
	fields []Field,
	goTypes map[string]string,
	dartTypes map[string]string,
	allowFile bool,
) error {
	names := make(map[string]struct{}, len(fields))
	for index, field := range fields {
		fieldPath := fmt.Sprintf("%s[%d]", path, index)
		if !identifierPattern.MatchString(field.Name) {
			return fmt.Errorf("codegen: %s.name %q is invalid", fieldPath, field.Name)
		}
		if _, exists := names[field.Name]; exists {
			return fmt.Errorf("codegen: duplicate field %q in %s", field.Name, path)
		}
		names[field.Name] = struct{}{}
		switch field.Type {
		case "string", "integer", "boolean":
			if field.Object != nil {
				return fmt.Errorf("codegen: %s.object requires type object", fieldPath)
			}
		case "file":
			if !allowFile {
				return fmt.Errorf("codegen: %s file fields are not allowed here", fieldPath)
			}
			if field.Object != nil {
				return fmt.Errorf("codegen: %s.object requires type object", fieldPath)
			}
			if field.Array {
				return fmt.Errorf("codegen: %s file arrays are not supported", fieldPath)
			}
		case "object":
			if field.Object == nil {
				return fmt.Errorf("codegen: %s.object is required for type object", fieldPath)
			}
			if field.Array {
				return fmt.Errorf("codegen: %s nested object arrays are not supported", fieldPath)
			}
			if err := validateObject(
				fieldPath+".object",
				*field.Object,
				goTypes,
				dartTypes,
				allowFile,
			); err != nil {
				return err
			}
		default:
			return fmt.Errorf("codegen: %s.type %q is unsupported", fieldPath, field.Type)
		}
		if field.Format != "" && (field.Type != "string" || field.Format != "date-time") {
			return fmt.Errorf("codegen: %s.format %q is unsupported", fieldPath, field.Format)
		}
		if field.MinLength < 0 || (field.MinLength > 0 && (field.Type != "string" || field.Array)) {
			return fmt.Errorf("codegen: %s.minLength requires a scalar string", fieldPath)
		}
		if field.MaxLength < 0 || (field.MaxLength > 0 && (field.Type != "string" || field.Array)) {
			return fmt.Errorf("codegen: %s.maxLength requires a scalar string", fieldPath)
		}
		if field.MaxLength > 0 && field.MinLength > field.MaxLength {
			return fmt.Errorf("codegen: %s.minLength must not exceed maxLength", fieldPath)
		}
		if field.Trim && (field.Type != "string" || field.Array) {
			return fmt.Errorf("codegen: %s.trim requires a scalar string", fieldPath)
		}
		if len(field.Enum) > 0 {
			if field.Type != "string" || field.Array {
				return fmt.Errorf("codegen: %s.enum requires a scalar string", fieldPath)
			}
			values := make(map[string]struct{}, len(field.Enum))
			for enumIndex, value := range field.Enum {
				if value == "" {
					return fmt.Errorf("codegen: %s.enum[%d] must not be empty", fieldPath, enumIndex)
				}
				if _, exists := values[value]; exists {
					return fmt.Errorf("codegen: duplicate enum value %q in %s", value, fieldPath)
				}
				values[value] = struct{}{}
			}
		}
		if field.Type == "object" && (field.Format != "" || field.MinLength != 0 || field.MaxLength != 0 || field.Trim || len(field.Enum) > 0) {
			return fmt.Errorf("codegen: %s object field has scalar-only options", fieldPath)
		}
	}
	return nil
}
