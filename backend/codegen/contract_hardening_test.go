package codegen

import (
	"strings"
	"testing"
)

func TestGenerateHardensPayloadAndDateTimeArrays(t *testing.T) {
	schema := Schema{
		SchemaVersion:   SupportedSchemaVersion,
		ProtocolVersion: 1,
		Methods: []Method{{
			Name:       "calendar.sync",
			ClientName: "syncCalendar",
			Params: &Object{
				GoType:   "SyncCalendarRequest",
				DartType: "SyncCalendarRequest",
				Fields: []Field{
					{Name: "name", Type: "string", MinLength: 1, MaxLength: 20, Trim: true},
					{Name: "attempts", Type: "integer", Nullable: true, Minimum: integerPointer(0), Maximum: integerPointer(5)},
					{Name: "starts", Type: "string", Format: "date-time", Array: true},
					{Name: "optionalEnds", Type: "string", Format: "date-time", Array: true, Nullable: true},
				},
			},
			Result: Object{
				GoType:   "SyncCalendarResponse",
				DartType: "SyncCalendarResult",
				Fields: []Field{
					{Name: "starts", Type: "string", Format: "date-time", Array: true},
					{Name: "optionalEnds", Type: "string", Format: "date-time", Array: true, Nullable: true},
				},
			},
		}},
	}

	outputs, err := Generate(schema)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	requests := generatedContent(t, outputs, GoRequestsPath)
	for _, fragment := range []string{
		"ValidatePayload(payload []byte) error",
		"framework.ValidateRequestPayload[SyncCalendarRequest](payload)",
		"func (request *SyncCalendarRequest) Normalize()",
		"framework.MinLength(1",
		"Attempts     *int",
		"framework.Optional(framework.Minimum(0",
		"framework.Optional(framework.Maximum(5",
		"Starts       []string",
		"OptionalEnds *[]string",
	} {
		if !strings.Contains(requests, fragment) {
			t.Errorf("Go requests do not contain %q:\n%s", fragment, requests)
		}
	}

	dart := generatedContent(t, outputs, DartClientPath)
	for _, fragment := range []string{
		"final List<DateTime> starts;",
		"final List<DateTime>? optionalEnds;",
		"starts.map((item) => item.toUtc().toIso8601String())",
		"optionalEnds?.map((item) => item.toUtc().toIso8601String())",
		"_requireDateTimeListField(result, 'starts')",
		"_optionalDateTimeListField(result, 'optionalEnds')",
		"_requireListField<String>(data, field).map(DateTime.parse)",
	} {
		if !strings.Contains(dart, fragment) {
			t.Errorf("Dart client does not contain %q:\n%s", fragment, dart)
		}
	}
}

func integerPointer(value int) *int {
	return &value
}

func TestSchemaRejectsInvalidMinimumLength(t *testing.T) {
	schema := Schema{
		SchemaVersion:   SupportedSchemaVersion,
		ProtocolVersion: 1,
		Methods: []Method{{
			Name:       "test.read",
			ClientName: "read",
			Result: Object{
				GoType:   "Result",
				DartType: "ResultModel",
				Fields: []Field{{
					Name: "value", Type: "string", MinLength: 2, MaxLength: 1,
				}},
			},
		}},
	}

	err := schema.Validate()
	if err == nil || !strings.Contains(err.Error(), "minLength must not exceed maxLength") {
		t.Fatalf("validation error = %v", err)
	}
}
