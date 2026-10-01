package codegen

import (
	"strings"
	"testing"
)

func TestGeneratedMemberNamesRespectObjectScope(t *testing.T) {
	object := func(name, field string) Object {
		return Object{GoType: name, DartType: name, Fields: []Field{{Name: field, Type: "string"}}}
	}
	tests := []struct {
		name   string
		change func(*Schema)
		want   string
	}{
		{
			name:   "request encoder collision",
			change: func(schema *Schema) { schema.Methods[0].Params.Fields[0].Name = "to_json" },
			want:   `conflicts with generated Dart member "toJson"`,
		},
		{
			name:   "request payload validator collision",
			change: func(schema *Schema) { schema.Methods[0].Params.Fields[0].Name = "validate_payload" },
			want:   `conflicts with generated Go method "ValidatePayload"`,
		},
		{
			name: "nested request encoder collision",
			change: func(schema *Schema) {
				nested := object("NestedRequest", "to_json")
				schema.Methods[0].Params.Fields = []Field{{Name: "nested", Type: "object", Object: &nested}}
			},
			want: `conflicts with generated Dart member "toJson"`,
		},
		{
			name: "reusable response encoder collision",
			change: func(schema *Schema) {
				schema.Types = []NamedObject{{Name: "entry", Object: object("Entry", "to_json")}}
				schema.Methods[0].Result.Fields = []Field{{Name: "entry", Type: "object", Ref: "entry"}}
			},
			want: `conflicts with generated Dart member "toJson"`,
		},
		{
			name: "reusable request payload validator collision",
			change: func(schema *Schema) {
				schema.Types = []NamedObject{{Name: "entry", Object: object("Entry", "validate_payload")}}
				schema.Methods[0].Params.Fields = []Field{{Name: "entry", Type: "object", Ref: "entry"}}
			},
			want: `conflicts with generated Go method "ValidatePayload"`,
		},
		{
			name: "normalizer collision",
			change: func(schema *Schema) {
				schema.Methods[0].Params.Fields = []Field{{Name: "normalize", Type: "string", Trim: true}}
			},
			want: `conflicts with generated Go method "Normalize"`,
		},
		{
			name: "rule validator collision",
			change: func(schema *Schema) {
				schema.Methods[0].Params.Fields = []Field{{Name: "validate", Type: "string", MinLength: 1}}
			},
			want: `conflicts with generated Go method "Validate"`,
		},
		{
			name: "result and meta share only Dart scope",
			change: func(schema *Schema) {
				schema.Methods[0].Result.Fields[0].Name = "foo"
				schema.Methods[0].Meta = []Field{{Name: "Foo", Type: "string"}}
			},
		},
		{
			name: "meta has no Go struct",
			change: func(schema *Schema) {
				schema.Methods[0].Meta = []Field{{Name: "foo", Type: "string"}, {Name: "Foo", Type: "string"}}
			},
		},
		{
			name:   "top level result has no encoder",
			change: func(schema *Schema) { schema.Methods[0].Result.Fields[0].Name = "to_json" },
		},
		{
			name: "inline response has no encoder",
			change: func(schema *Schema) {
				nested := object("NestedResponse", "to_json")
				schema.Methods[0].Result.Fields = []Field{{Name: "nested", Type: "object", Object: &nested}}
			},
		},
		{
			name: "named factory can coexist with an instance field",
			change: func(schema *Schema) {
				schema.Types = []NamedObject{{Name: "entry", Object: object("Entry", "from_json")}}
				schema.Methods[0].Result.Fields = []Field{{Name: "entry", Type: "object", Ref: "entry"}}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := object("ProbeRequest", "value")
			schema := Schema{
				SchemaVersion: SupportedSchemaVersion, ProtocolVersion: 1,
				Methods: []Method{{Name: "names.probe", ClientName: "probe", Params: &request, Result: object("ProbeResult", "value")}},
			}
			test.change(&schema)
			_, err := Generate(schema)
			if test.want == "" {
				if err != nil {
					t.Fatalf("valid object scope rejected: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}
