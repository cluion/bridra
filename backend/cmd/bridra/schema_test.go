package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cluion/bridra/backend/codegen"
)

func TestSchemaCheckReportsCompatibleSchemas(t *testing.T) {
	root := t.TempDir()
	baselinePath := writeSchemaCheckFixture(t, root, "baseline.json", schemaCheckFixture(1))
	current := schemaCheckFixture(1)
	current.Methods = append(current.Methods, codegen.Method{
		Name:       "demo.status",
		ClientName: "status",
		Result: codegen.Object{
			GoType:   "StatusResponse",
			DartType: "StatusResult",
			Fields:   []codegen.Field{{Name: "ready", Type: "boolean"}},
		},
	})
	currentPath := writeSchemaCheckFixture(t, root, "current.json", current)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := run([]string{
		"schema", "check",
		"--against", baselinePath,
		"--schema", currentPath,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("schema check: %v, stderr: %s", err, stderr.String())
	}
	for _, expected := range []string{
		"Status: compatible",
		"Breaking wire changes: 0",
		"method_added",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("stdout = %q, want %q", stdout.String(), expected)
		}
	}
}

func TestSchemaCheckRejectsBreakingChangeWithoutProtocolBump(t *testing.T) {
	root := t.TempDir()
	baselinePath := writeSchemaCheckFixture(t, root, "baseline.json", schemaCheckFixture(1))
	current := schemaCheckFixture(1)
	current.Methods[0].Result.Fields[0].Type = "boolean"
	currentPath := writeSchemaCheckFixture(t, root, "current.json", current)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := run([]string{
		"schema", "check",
		"--against", baselinePath,
		"--schema", currentPath,
	}, &stdout, &stderr)
	if !errors.Is(err, errSchemaIncompatible) {
		t.Fatalf("error = %v, want errSchemaIncompatible", err)
	}
	for _, expected := range []string{
		"Status: incompatible",
		"field_shape_changed",
		"Required protocolVersion: 2 or newer",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("stdout = %q, want %q", stdout.String(), expected)
		}
	}
}

func TestSchemaCheckAcceptsVersionedBreakingChange(t *testing.T) {
	root := t.TempDir()
	baselinePath := writeSchemaCheckFixture(t, root, "baseline.json", schemaCheckFixture(4))
	current := schemaCheckFixture(5)
	current.Methods[0].Result.Fields[0].Type = "boolean"
	currentPath := writeSchemaCheckFixture(t, root, "current.json", current)
	var stdout bytes.Buffer

	err := run([]string{
		"schema", "check",
		"--against", baselinePath,
		"--schema", currentPath,
	}, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("schema check: %v", err)
	}
	if !strings.Contains(stdout.String(), "Status: versioned_break") ||
		!strings.Contains(stdout.String(), "Protocol bump isolates") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestSchemaCheckWritesJSONBeforeReturningIncompatibleError(t *testing.T) {
	root := t.TempDir()
	baselinePath := writeSchemaCheckFixture(t, root, "baseline.json", schemaCheckFixture(2))
	current := schemaCheckFixture(2)
	current.Methods = nil
	current.Methods = append(current.Methods, codegen.Method{
		Name:       "demo.other",
		ClientName: "other",
		Result: codegen.Object{
			GoType:   "OtherResponse",
			DartType: "OtherResult",
			Fields:   []codegen.Field{{Name: "value", Type: "string"}},
		},
	})
	currentPath := writeSchemaCheckFixture(t, root, "current.json", current)
	var stdout bytes.Buffer

	err := run([]string{
		"schema", "check",
		"--against", baselinePath,
		"--schema", currentPath,
		"--json",
	}, &stdout, &bytes.Buffer{})
	if !errors.Is(err, errSchemaIncompatible) {
		t.Fatalf("error = %v, want errSchemaIncompatible", err)
	}
	var report schemaCheckJSONReport
	if decodeErr := json.Unmarshal(stdout.Bytes(), &report); decodeErr != nil {
		t.Fatalf("decode JSON report: %v\n%s", decodeErr, stdout.String())
	}
	if report.ReportVersion != 1 || report.Status != codegen.SchemaIncompatible {
		t.Fatalf("report = %#v", report)
	}
	if report.MinimumProtocol != 3 || report.BreakingChanges != 1 {
		t.Fatalf("report = %#v, want protocol 3 and one breaking change", report)
	}
}

func TestSchemaCheckRequiresBaseline(t *testing.T) {
	err := run([]string{"schema", "check"}, &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "--against is required") {
		t.Fatalf("error = %v, want missing --against usage error", err)
	}
}

func writeSchemaCheckFixture(
	t *testing.T,
	root, name string,
	schema codegen.Schema,
) string {
	t.Helper()
	content, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, append(content, '\n'), 0o600); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	return path
}

func schemaCheckFixture(protocolVersion int) codegen.Schema {
	return codegen.Schema{
		SchemaVersion:   codegen.SupportedSchemaVersion,
		ProtocolVersion: protocolVersion,
		Methods: []codegen.Method{{
			Name:       "demo.echo",
			ClientName: "echo",
			Params: &codegen.Object{
				GoType:   "EchoRequest",
				DartType: "EchoRequest",
				Fields:   []codegen.Field{{Name: "value", Type: "string"}},
			},
			Result: codegen.Object{
				GoType:   "EchoResponse",
				DartType: "EchoResult",
				Fields:   []codegen.Field{{Name: "value", Type: "string"}},
			},
		}},
	}
}
