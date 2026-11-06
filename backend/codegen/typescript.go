package codegen

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

const TypeScriptClientPath = "frontend/src/bridra.generated.ts"
const typeScriptRuntimePath = "frontend/src/bridra.runtime.ts"

//go:embed typescript/runtime.ts
var typeScriptRuntime string

// DTO names use a prefix so schema names cannot shadow TypeScript keywords or runtime imports.
func renderTypeScriptClient(schema Schema) ([]byte, error) {
	var out strings.Builder
	writeGeneratedHeader(&out, "//")
	out.WriteString("import * as rpc from './bridra.runtime';\nexport { HttpError, RpcError } from './bridra.runtime';\n\n")
	fmt.Fprintf(&out, "export const protocolVersion = %d;\n\n", schema.ProtocolVersion)
	healthName := ""
	seen := make(map[string]bool)
	for _, m := range schema.Methods {
		if m.Stream {
			return nil, fmt.Errorf("codegen: streaming method %s is not supported by the Web client", m.Name)
		}
		if m.ClientName == "constructor" || m.ClientName == "then" {
			return nil, fmt.Errorf("codegen: client method %q conflicts with TypeScript client behavior", m.ClientName)
		}
		if m.Name == "system.health" && m.Params == nil {
			for _, f := range m.Result.Fields {
				if f.Name == "protocolVersion" && f.Type == "integer" && !f.Nullable && !f.Array {
					healthName = m.ClientName
				}
			}
		}
		if m.Params != nil {
			if err := writeTSObject(&out, seen, *m.Params, nil); err != nil {
				return nil, err
			}
		}
		if err := writeTSObject(&out, seen, m.Result, m.Meta); err != nil {
			return nil, err
		}
	}
	if healthName == "" {
		return nil, fmt.Errorf("codegen: the Web client requires system.health with an integer protocolVersion and no params")
	}
	out.WriteString("export class BridraClient {\n  #verified = false;\n")
	for _, m := range schema.Methods {
		param, body := "", ""
		if m.Params != nil {
			param = "params: DTO" + m.Params.DartType
			body = fmt.Sprintf(", decode%s(params, %q)", m.Params.DartType, m.Name+".params")
		}
		fmt.Fprintf(&out, "\n  async %s(%s): Promise<DTO%s> {\n", m.ClientName, param, m.Result.DartType)
		if m.ClientName == healthName {
			out.WriteString("    this.#verified = false;\n")
		} else {
			fmt.Fprintf(&out, "    if (!this.#verified) await this.%s();\n", healthName)
		}
		fmt.Fprintf(&out, "    const reply = await rpc.call(%q%s);\n", m.Name, body)
		meta := ""
		if len(m.Meta) != 0 {
			meta = ", reply.meta"
		}
		fmt.Fprintf(&out, "    const result = decode%s(reply.result, %q%s);\n", m.Result.DartType, m.Name, meta)
		if m.ClientName == healthName {
			out.WriteString("    if (result.protocolVersion !== protocolVersion) throw new Error(`Protocol mismatch: expected ${protocolVersion}, received ${result.protocolVersion}`);\n    this.#verified = true;\n")
		}
		out.WriteString("    return result;\n  }\n")
	}
	out.WriteString("}\n")
	return []byte(out.String()), nil
}

func writeTSObject(out *strings.Builder, seen map[string]bool, object Object, meta []Field) error {
	if seen[object.DartType] {
		return nil
	}
	seen[object.DartType] = true
	fields := appendFields(object.Fields, meta)
	for _, f := range fields {
		if f.Type == "file" {
			return fmt.Errorf("codegen: file field %s.%s is not supported by the Web client", object.DartType, f.Name)
		}
		if f.Object != nil {
			if err := writeTSObject(out, seen, *f.Object, nil); err != nil {
				return err
			}
		}
	}
	fmt.Fprintf(out, "export interface DTO%s {\n", object.DartType)
	for _, f := range fields {
		typ, _ := tsFieldType(f)
		optional := ""
		if f.Nullable {
			optional = "?"
		}
		fmt.Fprintf(out, "  %s%s: %s;\n", f.Name, optional, typ)
	}
	out.WriteString("}\n")
	metaArg := ""
	if len(meta) > 0 {
		metaArg = ", metadata: unknown"
	}
	fmt.Fprintf(out, "function decode%s(value: unknown, label: string%s): DTO%s {\n", object.DartType, metaArg, object.DartType)
	out.WriteString("  const data = rpc.record(value, label);\n")
	if len(meta) > 0 {
		out.WriteString("  const meta = rpc.record(metadata ?? {}, label + '.meta');\n")
	}
	out.WriteString("  return {\n")
	for i, f := range fields {
		source := "data"
		if i >= len(object.Fields) {
			source = "meta"
		}
		_, decoder := tsFieldType(f)
		fmt.Fprintf(out, "    %s: (%s)(rpc.field(%s, %q), label + %q),\n", f.Name, decoder, source, f.Name, "."+f.Name)
	}
	out.WriteString("  };\n}\n\n")
	return nil
}

func tsFieldType(f Field) (string, string) {
	var typ, decoder string
	switch f.Type {
	case "integer":
		typ, decoder = "number", "rpc.integerValue"
	case "number":
		typ, decoder = "number", "rpc.numberValue"
	case "boolean":
		typ, decoder = "boolean", "rpc.booleanValue"
	case "object":
		typ, decoder = "DTO"+f.Object.DartType, "decode"+f.Object.DartType
	case "string":
		typ, decoder = "string", "rpc.stringValue"
		if f.Format == "date-time" {
			decoder = "rpc.dateTimeValue"
		}
		if len(f.Enum) > 0 {
			literals := make([]string, len(f.Enum))
			for i, v := range f.Enum {
				b, _ := json.Marshal(v)
				literals[i] = string(b)
			}
			typ = strings.Join(literals, " | ")
			decoder = "(v: unknown, k: string) => rpc.enumValue(v, k, [" + strings.Join(literals, ", ") + "])"
		}
	}
	if f.Array {
		typ = "Array<" + typ + ">"
		decoder = "(v: unknown, k: string) => rpc.arrayValue(v, k, " + decoder + ")"
	}
	if f.Nullable {
		typ += " | null"
		decoder = "(v: unknown, k: string) => rpc.nullableValue(v, k, " + decoder + ")"
	}
	return typ, decoder
}
