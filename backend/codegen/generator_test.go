package codegen

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGeneratedContractMatchesCheckedInGoldenFiles(t *testing.T) {
	root := repositoryRoot(t)
	schema, err := LoadSchema(filepath.Join(root, "schema", "bridra.json"))
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}
	outputs, err := Generate(schema)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	for _, output := range outputs {
		golden, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(output.Path)))
		if err != nil {
			t.Fatalf("read golden %s: %v", output.Path, err)
		}
		if !bytes.Equal(output.Content, golden) {
			t.Errorf("generated output %s does not match its checked-in golden file", output.Path)
		}
	}
}

func TestGenerateSupportsCustomRuntimeImports(t *testing.T) {
	schema := Schema{
		SchemaVersion:   SupportedSchemaVersion,
		ProtocolVersion: 1,
		Methods: []Method{{
			Name:       "demo.echo",
			ClientName: "echo",
			Params: &Object{
				GoType:   "EchoRequest",
				DartType: "EchoRequest",
				Fields: []Field{{
					Name: "message",
					Type: "string",
				}},
			},
			Result: Object{
				GoType:   "EchoResponse",
				DartType: "EchoResult",
				Fields: []Field{{
					Name: "message",
					Type: "string",
				}},
			},
		}},
	}
	outputs, err := GenerateWithOptions(schema, Options{
		GoFrameworkImport: "example.test/bridra/framework",
		DartRuntimeImport: "package:custom_bridra/custom_bridra.dart",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	requests := generatedContent(t, outputs, GoRequestsPath)
	if !strings.Contains(requests, "\"example.test/bridra/framework\"") {
		t.Fatalf("Go requests use the wrong framework import:\n%s", requests)
	}
	dart := generatedContent(t, outputs, DartClientPath)
	if !strings.Contains(dart, "import 'package:custom_bridra/custom_bridra.dart';") {
		t.Fatalf("Dart client uses the wrong runtime import:\n%s", dart)
	}
	for _, fragment := range []string{
		"RpcCancellationToken? cancellationToken",
		"cancellationToken: cancellationToken",
	} {
		if !strings.Contains(dart, fragment) {
			t.Fatalf("Dart client does not contain %q:\n%s", fragment, dart)
		}
	}
}

func TestGenerateSupportsTypedStreamingMethods(t *testing.T) {
	schema := Schema{
		SchemaVersion:   SupportedSchemaVersion,
		ProtocolVersion: 2,
		Methods: []Method{{
			Name:       "reports.build",
			ClientName: "buildReport",
			Stream:     true,
			Result: Object{
				GoType:   "ReportPageResponse",
				DartType: "ReportPage",
				Fields: []Field{{
					Name: "page",
					Type: "integer",
				}},
			},
		}},
	}

	outputs, err := Generate(schema)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	dart := generatedContent(t, outputs, DartClientPath)
	for _, fragment := range []string{
		"Stream<RpcStreamEvent<ReportPage>> buildReport(",
		"final events = _client.stream(",
		"Duration timeout = const Duration(minutes: 5)",
		"timeout: timeout",
		"await for (final event in events)",
		"yield RpcStreamProgress<ReportPage>(",
		"final data = event as RpcStreamData<RpcReply>;",
		"yield RpcStreamData(",
	} {
		if !strings.Contains(dart, fragment) {
			t.Errorf("Dart streaming client does not contain %q:\n%s", fragment, dart)
		}
	}
}

func TestGenerateSupportsTypedFileResponses(t *testing.T) {
	schema := Schema{
		SchemaVersion:   SupportedSchemaVersion,
		ProtocolVersion: 2,
		Methods: []Method{{
			Name:       "reports.export",
			ClientName: "exportReport",
			Result: Object{
				GoType:   "ExportReportResponse",
				DartType: "ExportReportResult",
				Fields: []Field{
					{Name: "file", Type: "file"},
					{Name: "preview", Type: "file", Nullable: true},
				},
			},
		}},
	}

	outputs, err := GenerateWithOptions(schema, Options{
		GoFrameworkImport: "example.test/bridra/framework",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	responses := generatedContent(t, outputs, GoResponsesPath)
	for _, fragment := range []string{
		`"example.test/bridra/framework"`,
		"File    framework.FileReference",
		"Preview *framework.FileReference",
	} {
		if !strings.Contains(responses, fragment) {
			t.Errorf("Go responses do not contain %q:\n%s", fragment, responses)
		}
	}
	dart := generatedContent(t, outputs, DartClientPath)
	for _, fragment := range []string{
		"final RpcFileReference file;",
		"final RpcFileReference? preview;",
		"_requireFileField(result, 'file')",
		"_optionalFileField(result, 'preview')",
		"RpcFileReference.fromJson",
	} {
		if !strings.Contains(dart, fragment) {
			t.Errorf("Dart client does not contain %q:\n%s", fragment, dart)
		}
	}
}

func TestCheckReportsMissingAndStaleOutputs(t *testing.T) {
	root := t.TempDir()
	outputs := []Output{
		{Path: "generated/first.txt", Content: []byte("first\n")},
		{Path: "generated/second.txt", Content: []byte("second\n")},
	}

	if err := Write(root, outputs); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := Check(root, outputs); err != nil {
		t.Fatalf("check fresh output: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "generated", "first.txt"),
		[]byte("changed\n"),
		0o644,
	); err != nil {
		t.Fatalf("change output: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "generated", "second.txt")); err != nil {
		t.Fatalf("remove output: %v", err)
	}

	err := Check(root, outputs)
	if !errors.Is(err, ErrGeneratedFilesStale) {
		t.Fatalf("check error = %v, want ErrGeneratedFilesStale", err)
	}
	if !strings.Contains(err.Error(), "first.txt") || !strings.Contains(err.Error(), "second.txt") {
		t.Fatalf("check error does not identify stale outputs: %v", err)
	}
}

func TestCheckAcceptsWindowsLineEndings(t *testing.T) {
	root := t.TempDir()
	outputs := []Output{
		{Path: "generated/first.txt", Content: []byte("first\nsecond\n")},
	}
	path := filepath.Join(root, "generated", "first.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create generated directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("first\r\nsecond\r\n"), 0o644); err != nil {
		t.Fatalf("write CRLF output: %v", err)
	}

	if err := Check(root, outputs); err != nil {
		t.Fatalf("check CRLF output: %v", err)
	}
}

func TestSchemaRejectsDuplicateMethods(t *testing.T) {
	result := Object{
		GoType:   "Result",
		DartType: "ResultModel",
		Fields:   []Field{{Name: "value", Type: "string"}},
	}
	schema := Schema{
		SchemaVersion:   SupportedSchemaVersion,
		ProtocolVersion: 1,
		Methods: []Method{
			{Name: "test.read", ClientName: "read", Result: result},
			{Name: "test.read", ClientName: "readAgain", Result: Object{
				GoType:   "OtherResult",
				DartType: "OtherResultModel",
				Fields:   []Field{{Name: "value", Type: "string"}},
			}},
		},
	}

	if err := schema.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate method") {
		t.Fatalf("validation error = %v, want duplicate method", err)
	}
}

func TestGenerateSupportsNullableEnumAndNestedObjects(t *testing.T) {
	schema := Schema{
		SchemaVersion:   SupportedSchemaVersion,
		ProtocolVersion: 1,
		Methods: []Method{{
			Name:       "profile.update",
			ClientName: "updateProfile",
			Params: &Object{
				GoType:   "UpdateProfileRequest",
				DartType: "UpdateProfileRequest",
				Fields: []Field{
					{Name: "mode", Type: "string", Nullable: true, Enum: []string{"public", "private"}},
					{
						Name: "details", Type: "object", Nullable: true,
						Object: &Object{
							GoType:   "ProfileDetailsRequest",
							DartType: "ProfileDetailsRequest",
							Fields:   []Field{{Name: "label", Type: "string", MaxLength: 20}},
						},
					},
				},
			},
			Result: Object{
				GoType:   "UpdateProfileResponse",
				DartType: "UpdateProfileResult",
				Fields: []Field{{
					Name: "details", Type: "object", Nullable: true,
					Object: &Object{
						GoType:   "ProfileDetailsResponse",
						DartType: "ProfileDetails",
						Fields:   []Field{{Name: "label", Type: "string"}},
					},
				}},
			},
		}},
	}

	outputs, err := Generate(schema)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	requests := generatedContent(t, outputs, GoRequestsPath)
	for _, fragment := range []string{
		"Mode    *string",
		"framework.Optional(framework.OneOf",
		"framework.OptionalNestedField",
		"Details *ProfileDetailsRequest",
	} {
		if !strings.Contains(requests, fragment) {
			t.Errorf("Go requests do not contain %q:\n%s", fragment, requests)
		}
	}
	dart := generatedContent(t, outputs, DartClientPath)
	for _, fragment := range []string{
		"final String? mode;",
		"final ProfileDetailsRequest? details;",
		"ProfileDetails.fromJson",
		"_optionalObjectField<ProfileDetails>",
	} {
		if !strings.Contains(dart, fragment) {
			t.Errorf("Dart client does not contain %q:\n%s", fragment, dart)
		}
	}
}

func TestSchemaRejectsInvalidEnumAndNestedObjectDefinitions(t *testing.T) {
	base := func(field Field) Schema {
		return Schema{
			SchemaVersion:   SupportedSchemaVersion,
			ProtocolVersion: 1,
			Methods: []Method{{
				Name:       "test.read",
				ClientName: "read",
				Result: Object{
					GoType:   "Result",
					DartType: "ResultModel",
					Fields:   []Field{field},
				},
			}},
		}
	}
	tests := []struct {
		name  string
		field Field
		want  string
	}{
		{
			name:  "duplicate enum",
			field: Field{Name: "state", Type: "string", Enum: []string{"open", "open"}},
			want:  "duplicate enum value",
		},
		{
			name:  "enum on integer",
			field: Field{Name: "state", Type: "integer", Enum: []string{"1"}},
			want:  "enum requires a scalar string",
		},
		{
			name:  "object without definition",
			field: Field{Name: "profile", Type: "object"},
			want:  "object is required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := base(test.field).Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestSchemaRestrictsFileFieldsToScalarResponses(t *testing.T) {
	result := Object{
		GoType:   "Result",
		DartType: "ResultModel",
		Fields:   []Field{{Name: "value", Type: "string"}},
	}
	schemaWithParams := func(field Field) Schema {
		return Schema{
			SchemaVersion:   SupportedSchemaVersion,
			ProtocolVersion: 1,
			Methods: []Method{{
				Name:       "files.write",
				ClientName: "writeFile",
				Params: &Object{
					GoType:   "WriteFileRequest",
					DartType: "WriteFileRequest",
					Fields:   []Field{field},
				},
				Result: result,
			}},
		}
	}
	if err := schemaWithParams(Field{Name: "file", Type: "file"}).Validate(); err == nil ||
		!strings.Contains(err.Error(), "response-only") {
		t.Fatalf("request file validation error = %v", err)
	}

	schema := Schema{
		SchemaVersion:   SupportedSchemaVersion,
		ProtocolVersion: 1,
		Methods: []Method{{
			Name:       "files.read",
			ClientName: "readFile",
			Result: Object{
				GoType:   "ReadFileResponse",
				DartType: "ReadFileResult",
				Fields:   []Field{{Name: "files", Type: "file", Array: true}},
			},
		}},
	}
	if err := schema.Validate(); err == nil ||
		!strings.Contains(err.Error(), "file arrays are not supported") {
		t.Fatalf("file array validation error = %v", err)
	}
}

func generatedContent(t *testing.T, outputs []Output, path string) string {
	t.Helper()
	for _, output := range outputs {
		if output.Path == path {
			return string(output.Content)
		}
	}
	t.Fatalf("generated output %s not found", path)
	return ""
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve generator test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
