package codegen

import "testing"

func TestCompareSchemasAcceptsCompatibleWireChanges(t *testing.T) {
	baseline := compatibilityTestSchema(1)
	current := compatibilityTestSchema(1)
	current.Methods[0].Result.Fields = append(
		current.Methods[0].Result.Fields,
		Field{Name: "detail", Type: "string", Nullable: true},
	)
	current.Methods = append(current.Methods, Method{
		Name:       "demo.status",
		ClientName: "status",
		Result: Object{
			GoType:   "StatusResponse",
			DartType: "StatusResult",
			Fields:   []Field{{Name: "ready", Type: "boolean"}},
		},
	})

	report, err := CompareSchemas(baseline, current)
	if err != nil {
		t.Fatalf("compare schemas: %v", err)
	}
	if report.Status != SchemaCompatible {
		t.Fatalf("status = %q, want compatible", report.Status)
	}
	if report.ProtocolBumpRequired || report.BreakingChanges != 0 {
		t.Fatalf("report = %#v, want no protocol bump", report)
	}
	for _, code := range []string{"nullable_response_field_added", "method_added"} {
		if !compatibilityHasChange(report, code) {
			t.Errorf("changes = %#v, want %q", report.Changes, code)
		}
	}
}

func TestCompareSchemasRequiresProtocolBumpForBreakingWireChanges(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Schema)
		code   string
	}{
		{
			name: "remove method",
			change: func(schema *Schema) {
				schema.Methods = schema.Methods[:1]
			},
			code: "method_removed",
		},
		{
			name: "change streaming mode",
			change: func(schema *Schema) {
				schema.Methods[0].Stream = true
			},
			code: "streaming_changed",
		},
		{
			name: "add method params",
			change: func(schema *Schema) {
				schema.Methods[1].Params = &Object{
					GoType:   "PingRequest",
					DartType: "PingRequest",
					Fields:   []Field{{Name: "note", Type: "string", Nullable: true}},
				}
			},
			code: "params_added",
		},
		{
			name: "remove method params",
			change: func(schema *Schema) {
				schema.Methods[0].Params = nil
			},
			code: "params_removed",
		},
		{
			name: "add nullable request field",
			change: func(schema *Schema) {
				schema.Methods[0].Params.Fields = append(
					schema.Methods[0].Params.Fields,
					Field{Name: "note", Type: "string", Nullable: true},
				)
			},
			code: "field_added",
		},
		{
			name: "change request rules",
			change: func(schema *Schema) {
				schema.Methods[0].Params.Fields[0].MaxLength = 32
			},
			code: "request_rules_changed",
		},
		{
			name: "change response field shape",
			change: func(schema *Schema) {
				schema.Methods[0].Result.Fields[0].Type = "boolean"
			},
			code: "field_shape_changed",
		},
		{
			name: "add required response field",
			change: func(schema *Schema) {
				schema.Methods[0].Result.Fields = append(
					schema.Methods[0].Result.Fields,
					Field{Name: "revision", Type: "integer"},
				)
			},
			code: "field_added",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline := compatibilityTestSchema(1)
			current := compatibilityTestSchema(1)
			test.change(&current)

			report, err := CompareSchemas(baseline, current)
			if err != nil {
				t.Fatalf("compare schemas: %v", err)
			}
			if report.Status != SchemaIncompatible {
				t.Fatalf("status = %q, want incompatible", report.Status)
			}
			if !report.ProtocolBumpRequired || report.MinimumProtocolVersion != 2 {
				t.Fatalf("report = %#v, want protocol 2", report)
			}
			if !compatibilityHasChange(report, test.code) {
				t.Fatalf("changes = %#v, want %q", report.Changes, test.code)
			}
		})
	}
}

func TestCompareSchemasRequiresProtocolBumpForIntegerRequestBoundChange(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Field)
	}{
		{
			name: "tighten maximum",
			change: func(field *Field) {
				field.Maximum = integerPointer(5)
			},
		},
		{
			name: "add explicit zero minimum",
			change: func(field *Field) {
				field.Minimum = integerPointer(0)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline := compatibilityTestSchema(1)
			current := compatibilityTestSchema(1)
			for _, schema := range []*Schema{&baseline, &current} {
				schema.Methods[0].Params.Fields = append(
					schema.Methods[0].Params.Fields,
					Field{Name: "attempts", Type: "integer", Maximum: integerPointer(10)},
				)
			}
			test.change(&current.Methods[0].Params.Fields[1])

			report, err := CompareSchemas(baseline, current)
			if err != nil {
				t.Fatalf("compare schemas: %v", err)
			}
			if report.Status != SchemaIncompatible ||
				!compatibilityHasChange(report, "request_rules_changed") {
				t.Fatalf("report = %#v, want integer request rule change", report)
			}
		})
	}
}

func TestCompareSchemasAcceptsNullableResponseFieldRemoval(t *testing.T) {
	baseline := compatibilityTestSchema(1)
	current := compatibilityTestSchema(1)
	current.Methods[0].Result.Fields = current.Methods[0].Result.Fields[:1]

	report, err := CompareSchemas(baseline, current)
	if err != nil {
		t.Fatalf("compare schemas: %v", err)
	}
	if report.Status != SchemaCompatible || report.BreakingChanges != 0 {
		t.Fatalf("report = %#v, want compatible nullable removal", report)
	}
	if !compatibilityHasChange(report, "nullable_response_field_removed") {
		t.Fatalf("changes = %#v, want nullable response removal", report.Changes)
	}
}

func TestCompareSchemasDetectsNestedRequestRuleChange(t *testing.T) {
	baseline := compatibilityTestSchema(1)
	current := compatibilityTestSchema(1)
	for _, schema := range []*Schema{&baseline, &current} {
		schema.Methods[0].Params.Fields = append(
			schema.Methods[0].Params.Fields,
			Field{
				Name: "profile", Type: "object", Nullable: true,
				Object: &Object{
					GoType:   "EchoProfileRequest",
					DartType: "EchoProfileRequest",
					Fields:   []Field{{Name: "label", Type: "string", MaxLength: 64}},
				},
			},
		)
	}
	current.Methods[0].Params.Fields[1].Object.Fields[0].MaxLength = 32

	report, err := CompareSchemas(baseline, current)
	if err != nil {
		t.Fatalf("compare schemas: %v", err)
	}
	if report.Status != SchemaIncompatible ||
		!compatibilityHasChange(report, "request_rules_changed") {
		t.Fatalf("report = %#v, want nested request rule change", report)
	}
}

func TestCompareSchemasAcceptsBreakingChangesWithProtocolBump(t *testing.T) {
	baseline := compatibilityTestSchema(3)
	current := compatibilityTestSchema(4)
	current.Methods[0].Params.Fields[0].Nullable = true

	report, err := CompareSchemas(baseline, current)
	if err != nil {
		t.Fatalf("compare schemas: %v", err)
	}
	if report.Status != SchemaVersionedBreak {
		t.Fatalf("status = %q, want versioned break", report.Status)
	}
	if !report.ProtocolBumpRequired || !report.ProtocolBumpPresent {
		t.Fatalf("report = %#v, want a present protocol bump", report)
	}
	if report.MinimumProtocolVersion != 4 || report.BreakingChanges != 1 {
		t.Fatalf("report = %#v, want one breaking change and protocol 4", report)
	}
}

func TestCompareSchemasRejectsProtocolRegression(t *testing.T) {
	report, err := CompareSchemas(compatibilityTestSchema(3), compatibilityTestSchema(2))
	if err != nil {
		t.Fatalf("compare schemas: %v", err)
	}
	if report.Status != SchemaIncompatible {
		t.Fatalf("status = %q, want incompatible", report.Status)
	}
	if report.ProtocolBumpRequired || report.BreakingChanges != 0 {
		t.Fatalf("report = %#v, protocol regression is not a schema diff", report)
	}
	if report.MinimumProtocolVersion != 3 {
		t.Fatalf("minimum protocol = %d, want 3", report.MinimumProtocolVersion)
	}
}

func TestCompareSchemasIgnoresOrderingAndGeneratedNames(t *testing.T) {
	baseline := compatibilityTestSchema(1)
	current := compatibilityTestSchema(1)
	current.Methods[0].ClientName = "renamedEcho"
	current.Methods[0].Params.GoType = "RenamedEchoRequest"
	current.Methods[0].Params.DartType = "RenamedEchoRequest"
	current.Methods[0].Result.GoType = "RenamedEchoResponse"
	current.Methods[0].Result.DartType = "RenamedEchoResult"
	current.Methods[0].Params.Fields[0].Enum = []string{"formal", "friendly"}
	current.Methods[0], current.Methods[1] = current.Methods[1], current.Methods[0]

	report, err := CompareSchemas(baseline, current)
	if err != nil {
		t.Fatalf("compare schemas: %v", err)
	}
	if report.Status != SchemaCompatible || len(report.Changes) != 0 {
		t.Fatalf("report = %#v, want no wire changes", report)
	}
}

func TestCompareSchemasResolvesReusableTypeWireShapes(t *testing.T) {
	baseline := compatibilityTestSchema(1)
	baseline.Types = []NamedObject{{
		Name: "entry",
		Object: Object{
			GoType:   "Entry",
			DartType: "Entry",
			Fields:   []Field{{Name: "value", Type: "string"}},
		},
	}}
	baseline.Methods[0].Result.Fields = []Field{{
		Name: "entries", Type: "object", Array: true, Ref: "entry",
	}}
	current := baseline
	current.Types = append([]NamedObject(nil), baseline.Types...)
	current.Types[0].Fields = append([]Field(nil), baseline.Types[0].Fields...)
	current.Types[0].Fields = append(
		current.Types[0].Fields,
		Field{Name: "required", Type: "boolean"},
	)

	report, err := CompareSchemas(baseline, current)
	if err != nil {
		t.Fatalf("compare schemas: %v", err)
	}
	if report.Status != SchemaIncompatible ||
		!compatibilityHasChange(report, "field_added") {
		t.Fatalf("report = %#v, want incompatible reusable shape change", report)
	}
}

func compatibilityTestSchema(protocolVersion int) Schema {
	return Schema{
		SchemaVersion:   SupportedSchemaVersion,
		ProtocolVersion: protocolVersion,
		Methods: []Method{
			{
				Name:       "demo.echo",
				ClientName: "echo",
				Params: &Object{
					GoType:   "EchoRequest",
					DartType: "EchoRequest",
					Fields: []Field{{
						Name: "message", Type: "string", MaxLength: 64,
						Enum: []string{"friendly", "formal"},
					}},
				},
				Result: Object{
					GoType:   "EchoResponse",
					DartType: "EchoResult",
					Fields: []Field{
						{Name: "message", Type: "string"},
						{Name: "note", Type: "string", Nullable: true},
					},
				},
			},
			{
				Name:       "demo.ping",
				ClientName: "ping",
				Result: Object{
					GoType:   "PingResponse",
					DartType: "PingResult",
					Fields:   []Field{{Name: "ok", Type: "boolean"}},
				},
			},
		},
	}
}

func compatibilityHasChange(report SchemaCompatibilityReport, code string) bool {
	for _, change := range report.Changes {
		if change.Code == code {
			return true
		}
	}
	return false
}
