package codegen

import (
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

const (
	GoProtocolPath           = "backend/framework/protocol_version.gen.go"
	GoRoutesPath             = "backend/app/contracts/bridra.gen.go"
	GoRequestsPath           = "backend/app/requests/bridra.gen.go"
	GoResponsesPath          = "backend/app/responses/bridra.gen.go"
	DartClientPath           = "lib/api/generated/bridra_api.g.dart"
	DefaultGoFrameworkImport = "github.com/cluion/bridra/backend/framework"
	DefaultDartRuntimeImport = "package:bridra_flutter/bridra_flutter.dart"
)

var ErrGeneratedFilesStale = errors.New("codegen: generated files are stale")

type Output struct {
	Path    string
	Content []byte
}

type Options struct {
	GoFrameworkImport string
	DartRuntimeImport string
}

func Generate(schema Schema) ([]Output, error) {
	return GenerateWithOptions(schema, Options{})
}

func GenerateWithOptions(schema Schema, options Options) ([]Output, error) {
	if err := schema.Validate(); err != nil {
		return nil, err
	}
	schema = schema.resolveReferences()
	if strings.TrimSpace(options.GoFrameworkImport) == "" {
		options.GoFrameworkImport = DefaultGoFrameworkImport
	}
	if strings.TrimSpace(options.DartRuntimeImport) == "" {
		options.DartRuntimeImport = DefaultDartRuntimeImport
	}
	if strings.ContainsAny(options.GoFrameworkImport, "\r\n\"") {
		return nil, errors.New("codegen: Go framework import contains invalid characters")
	}
	if strings.ContainsAny(options.DartRuntimeImport, "\r\n'") {
		return nil, errors.New("codegen: Dart runtime import contains invalid characters")
	}

	protocol, err := formatGo(GoProtocolPath, renderGoProtocol(schema))
	if err != nil {
		return nil, err
	}
	routes, err := formatGo(GoRoutesPath, renderGoRoutes(schema))
	if err != nil {
		return nil, err
	}
	requests, err := formatGo(GoRequestsPath, renderGoRequests(schema, options.GoFrameworkImport))
	if err != nil {
		return nil, err
	}
	responses, err := formatGo(
		GoResponsesPath,
		renderGoResponses(schema, options.GoFrameworkImport),
	)
	if err != nil {
		return nil, err
	}

	return []Output{
		{Path: GoProtocolPath, Content: protocol},
		{Path: GoRoutesPath, Content: routes},
		{Path: GoRequestsPath, Content: requests},
		{Path: GoResponsesPath, Content: responses},
		{Path: DartClientPath, Content: renderDartClient(schema, options.DartRuntimeImport)},
	}, nil
}

func renderGoProtocol(schema Schema) []byte {
	var output strings.Builder
	writeGeneratedHeader(&output, "//")
	output.WriteString("package framework\n\n")
	fmt.Fprintf(&output, "const ProtocolVersion = %d\n", schema.ProtocolVersion)
	return []byte(output.String())
}

func Write(root string, outputs []Output) error {
	for _, output := range outputs {
		path := filepath.Join(root, filepath.FromSlash(output.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("codegen: create output directory for %s: %w", output.Path, err)
		}
		if err := os.WriteFile(path, output.Content, 0o644); err != nil {
			return fmt.Errorf("codegen: write %s: %w", output.Path, err)
		}
	}
	return nil
}

func Check(root string, outputs []Output) error {
	stale := make([]string, 0)
	for _, output := range outputs {
		path := filepath.Join(root, filepath.FromSlash(output.Path))
		content, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				stale = append(stale, output.Path)
				continue
			}
			return fmt.Errorf("codegen: read %s: %w", output.Path, err)
		}
		if !bytes.Equal(normalizeLineEndings(content), normalizeLineEndings(output.Content)) {
			stale = append(stale, output.Path)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		return fmt.Errorf("%w: %s; run `make generate`", ErrGeneratedFilesStale, strings.Join(stale, ", "))
	}
	return nil
}

func normalizeLineEndings(content []byte) []byte {
	return bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
}

func formatGo(path string, source []byte) ([]byte, error) {
	formatted, err := format.Source(source)
	if err != nil {
		return nil, fmt.Errorf("codegen: format %s: %w", path, err)
	}
	return formatted, nil
}

func renderGoRoutes(schema Schema) []byte {
	var output strings.Builder
	writeGeneratedHeader(&output, "//")
	output.WriteString("package contracts\n\nconst (\n")
	groups := make(map[string]struct{})
	for _, method := range schema.Methods {
		group, _ := splitRouteMethod(method.Name)
		if _, exists := groups[group]; exists {
			continue
		}
		groups[group] = struct{}{}
		fmt.Fprintf(&output, "\t%s = %q\n", goRouteGroupConstant(group), group)
	}
	output.WriteString("\n")
	for _, method := range schema.Methods {
		_, action := splitRouteMethod(method.Name)
		fmt.Fprintf(&output, "\t%s = %q\n", goMethodConstant(method.Name), method.Name)
		fmt.Fprintf(&output, "\t%s = %q\n", goRouteActionConstant(method.Name), action)
	}
	output.WriteString(")\n")
	return []byte(output.String())
}

func renderGoRequests(schema Schema, frameworkImport string) []byte {
	var output strings.Builder
	writeGeneratedHeader(&output, "//")
	output.WriteString("package requests\n")

	hasParams := false
	hasTrim := false
	for _, method := range schema.Methods {
		if method.Params == nil {
			continue
		}
		hasParams = true
		hasTrim = hasTrim || objectHasTrim(*method.Params)
	}
	if hasParams {
		output.WriteString("\nimport (\n")
		if hasTrim {
			output.WriteString("\t\"strings\"\n\n")
		}
		fmt.Fprintf(&output, "\t%q\n)\n", frameworkImport)
	}

	emitted := make(map[string]struct{})
	for _, method := range schema.Methods {
		if method.Params == nil {
			continue
		}
		writeGoRequestObject(&output, *method.Params, emitted)
	}
	return []byte(output.String())
}

func renderGoResponses(schema Schema, frameworkImport string) []byte {
	var output strings.Builder
	writeGeneratedHeader(&output, "//")
	output.WriteString("package responses\n")
	if schemaHasFileResponses(schema) {
		fmt.Fprintf(&output, "\nimport %q\n", frameworkImport)
	}
	emitted := make(map[string]struct{})
	for _, method := range schema.Methods {
		for _, field := range method.Meta {
			if field.Object != nil {
				writeGoObject(&output, *field.Object, emitted)
			}
		}
		writeGoObject(&output, method.Result, emitted)
	}
	return []byte(output.String())
}

func writeGoRequestObject(
	output *strings.Builder,
	object Object,
	emitted map[string]struct{},
) {
	if _, exists := emitted[object.GoType]; exists {
		return
	}
	for _, field := range object.Fields {
		if field.Object != nil {
			writeGoRequestObject(output, *field.Object, emitted)
		}
	}
	emitted[object.GoType] = struct{}{}
	writeGoStruct(output, object)
	fmt.Fprintf(output, "\nfunc (%s) ValidatePayload(payload []byte) error {\n", object.GoType)
	fmt.Fprintf(output, "\treturn framework.ValidateRequestPayload[%s](payload)\n", object.GoType)
	output.WriteString("}\n")
	if objectHasTrim(object) {
		writeGoRequestNormalizer(output, object)
	}
	if !objectHasValidation(object) {
		return
	}

	registry := lowerIdentifier(object.GoType) + "Rules"
	fmt.Fprintf(output, "\nvar %s = framework.NewRuleRegistry[%s](\n", registry, object.GoType)
	for _, field := range object.Fields {
		if field.Object != nil {
			if !objectHasValidation(*field.Object) {
				continue
			}
			helper := "framework.NestedField"
			switch {
			case field.Array && field.Nullable:
				helper = "framework.OptionalNestedListField"
			case field.Array:
				helper = "framework.NestedListField"
			case field.Nullable:
				helper = "framework.OptionalNestedField"
			}
			fmt.Fprintf(output, "\t%s(\n", helper)
			fmt.Fprintf(output, "\t\t%q,\n", field.Name)
			fmt.Fprintf(
				output,
				"\t\tfunc(request %s) %s { return request.%s },\n",
				object.GoType,
				goType(field),
				goIdentifier(field.Name),
			)
			output.WriteString("\t),\n")
			continue
		}
		if field.MinLength == 0 && field.MaxLength == 0 && field.Minimum == nil &&
			field.Maximum == nil && len(field.Enum) == 0 {
			continue
		}
		output.WriteString("\tframework.ForField(\n")
		fmt.Fprintf(output, "\t\t%q,\n", field.Name)
		writeGoFieldSelector(output, object, field)
		if field.MinLength > 0 {
			rule := fmt.Sprintf(
				"framework.MinLength(%d, %q)",
				field.MinLength,
				fmt.Sprintf("%s must be at least %d %s.", humanName(field.Name), field.MinLength, characterUnit(field.MinLength)),
			)
			writeGoValueRule(output, field, rule)
		}
		if field.MaxLength > 0 {
			rule := fmt.Sprintf(
				"framework.MaxLength(%d, %q)",
				field.MaxLength,
				fmt.Sprintf("%s must be %d %s or fewer.", humanName(field.Name), field.MaxLength, characterUnit(field.MaxLength)),
			)
			writeGoValueRule(output, field, rule)
		}
		if field.Minimum != nil {
			rule := fmt.Sprintf(
				"framework.Minimum(%d, %q)",
				*field.Minimum,
				fmt.Sprintf("%s must be at least %d.", humanName(field.Name), *field.Minimum),
			)
			writeGoValueRule(output, field, rule)
		}
		if field.Maximum != nil {
			rule := fmt.Sprintf(
				"framework.Maximum(%d, %q)",
				*field.Maximum,
				fmt.Sprintf("%s must be %d or less.", humanName(field.Name), *field.Maximum),
			)
			writeGoValueRule(output, field, rule)
		}
		if len(field.Enum) > 0 {
			values := make([]string, 0, len(field.Enum))
			for _, value := range field.Enum {
				values = append(values, fmt.Sprintf("%q", value))
			}
			rule := fmt.Sprintf(
				"framework.OneOf(%q, %s)",
				fmt.Sprintf("%s must be one of: %s.", humanName(field.Name), strings.Join(field.Enum, ", ")),
				strings.Join(values, ", "),
			)
			writeGoValueRule(output, field, rule)
		}
		output.WriteString("\t),\n")
	}
	output.WriteString(")\n")
	fmt.Fprintf(output, "\nfunc (request %s) Validate() error {\n", object.GoType)
	if objectHasTrim(object) {
		output.WriteString("\trequest.Normalize()\n")
	}
	fmt.Fprintf(output, "\treturn %s.Validate(request)\n", registry)
	output.WriteString("}\n")
}

func writeGoRequestNormalizer(output *strings.Builder, object Object) {
	fmt.Fprintf(output, "\nfunc (request *%s) Normalize() {\n", object.GoType)
	for _, field := range object.Fields {
		fieldName := goIdentifier(field.Name)
		if field.Trim {
			if field.Nullable {
				fmt.Fprintf(output, "\tif request.%s != nil {\n", fieldName)
				fmt.Fprintf(output, "\t\tvalue := strings.TrimSpace(*request.%s)\n", fieldName)
				fmt.Fprintf(output, "\t\trequest.%s = &value\n", fieldName)
				output.WriteString("\t}\n")
			} else {
				fmt.Fprintf(output, "\trequest.%s = strings.TrimSpace(request.%s)\n", fieldName, fieldName)
			}
		}
		if field.Object == nil || !objectHasTrim(*field.Object) {
			continue
		}
		if field.Array && field.Nullable {
			fmt.Fprintf(output, "\tif request.%s != nil {\n", fieldName)
			fmt.Fprintf(output, "\t\tfor index := range *request.%s {\n", fieldName)
			fmt.Fprintf(output, "\t\t\t(*request.%s)[index].Normalize()\n", fieldName)
			output.WriteString("\t\t}\n\t}\n")
		} else if field.Array {
			fmt.Fprintf(output, "\tfor index := range request.%s {\n", fieldName)
			fmt.Fprintf(output, "\t\trequest.%s[index].Normalize()\n", fieldName)
			output.WriteString("\t}\n")
		} else if field.Nullable {
			fmt.Fprintf(output, "\tif request.%s != nil {\n", fieldName)
			fmt.Fprintf(output, "\t\trequest.%s.Normalize()\n", fieldName)
			output.WriteString("\t}\n")
		} else {
			fmt.Fprintf(output, "\trequest.%s.Normalize()\n", fieldName)
		}
	}
	output.WriteString("}\n")
}

func writeGoObject(output *strings.Builder, object Object, emitted map[string]struct{}) {
	if _, exists := emitted[object.GoType]; exists {
		return
	}
	for _, field := range object.Fields {
		if field.Object != nil {
			writeGoObject(output, *field.Object, emitted)
		}
	}
	emitted[object.GoType] = struct{}{}
	writeGoStruct(output, object)
}

func writeGoStruct(output *strings.Builder, object Object) {
	fmt.Fprintf(output, "\ntype %s struct {\n", object.GoType)
	for _, field := range object.Fields {
		tag := field.Name
		if field.Nullable {
			tag += ",omitempty"
		}
		fmt.Fprintf(
			output,
			"\t%s %s `json:\"%s\"`\n",
			goIdentifier(field.Name),
			goType(field),
			tag,
		)
	}
	output.WriteString("}\n")
}

func writeGoFieldSelector(output *strings.Builder, object Object, field Field) {
	fieldName := goIdentifier(field.Name)
	if !field.Trim {
		fmt.Fprintf(
			output,
			"\t\tfunc(request %s) %s { return request.%s },\n",
			object.GoType,
			goType(field),
			fieldName,
		)
		return
	}
	if !field.Nullable {
		fmt.Fprintf(
			output,
			"\t\tfunc(request %s) string { return strings.TrimSpace(request.%s) },\n",
			object.GoType,
			fieldName,
		)
		return
	}
	fmt.Fprintf(output, "\t\tfunc(request %s) *string {\n", object.GoType)
	fmt.Fprintf(output, "\t\t\tif request.%s == nil {\n", fieldName)
	output.WriteString("\t\t\t\treturn nil\n\t\t\t}\n")
	fmt.Fprintf(output, "\t\t\tvalue := strings.TrimSpace(*request.%s)\n", fieldName)
	output.WriteString("\t\t\treturn &value\n\t\t},\n")
}

func writeGoValueRule(output *strings.Builder, field Field, rule string) {
	if field.Nullable {
		rule = "framework.Optional(" + rule + ")"
	}
	fmt.Fprintf(output, "\t\t%s,\n", rule)
}

func renderDartClient(schema Schema, runtimeImport string) []byte {
	var output strings.Builder
	writeGeneratedHeader(&output, "//")
	fmt.Fprintf(&output, "import '%s';\n\n", runtimeImport)
	fmt.Fprintf(&output, "const supportedBackendProtocolVersion = %d;\n\n", schema.ProtocolVersion)
	output.WriteString("abstract final class BridraMethods {\n")
	for _, method := range schema.Methods {
		fmt.Fprintf(
			&output,
			"  static const %s = %s;\n",
			dartMethodConstant(method.Name),
			dartString(method.Name),
		)
	}
	output.WriteString("}\n")

	emitted := make(map[string]struct{})
	for _, definition := range schema.Types {
		writeDartReusableObject(&output, definition.Object, emitted)
	}
	for _, method := range schema.Methods {
		if method.Params != nil {
			writeDartRequest(&output, *method.Params, emitted)
		}
		writeDartResult(&output, method, emitted)
	}

	output.WriteString("\nabstract interface class BridraApi {\n")
	for _, method := range schema.Methods {
		fmt.Fprintf(&output, "  %s;\n", dartMethodSignature(method))
	}
	output.WriteString("}\n\n")
	output.WriteString("class BridraRpcApi implements BridraApi {\n")
	output.WriteString("  const BridraRpcApi(this._client);\n\n")
	output.WriteString("  final RpcClient _client;\n")
	for _, method := range schema.Methods {
		writeDartClientMethod(&output, method)
	}
	output.WriteString("}\n\n")
	writeDartDecoders(&output, schema)
	return []byte(strings.TrimRight(output.String(), "\n") + "\n")
}

func writeDartRequest(
	output *strings.Builder,
	object Object,
	emitted map[string]struct{},
) {
	if _, exists := emitted[object.DartType]; exists {
		return
	}
	for _, field := range object.Fields {
		if field.Object != nil {
			writeDartRequest(output, *field.Object, emitted)
		}
	}
	emitted[object.DartType] = struct{}{}
	fmt.Fprintf(output, "\nclass %s {\n", object.DartType)
	writeDartConstructor(output, object.DartType, object.Fields)
	output.WriteString("\n")
	for _, field := range object.Fields {
		fmt.Fprintf(output, "  final %s %s;\n", dartType(field), field.Name)
	}
	writeDartToJson(output, object.Fields)
	output.WriteString("}\n")
}

func writeDartResult(
	output *strings.Builder,
	method Method,
	emitted map[string]struct{},
) {
	for _, field := range appendFields(method.Result.Fields, method.Meta) {
		if field.Object != nil {
			writeDartResponseObject(output, *field.Object, emitted)
		}
	}
	fmt.Fprintf(output, "\nclass %s {\n", method.Result.DartType)
	fields := appendFields(method.Result.Fields, method.Meta)
	writeDartConstructor(output, method.Result.DartType, fields)
	output.WriteString("\n")
	for _, field := range fields {
		fmt.Fprintf(output, "  final %s %s;\n", dartType(field), field.Name)
	}
	output.WriteString("}\n")
}

func writeDartResponseObject(
	output *strings.Builder,
	object Object,
	emitted map[string]struct{},
) {
	if _, exists := emitted[object.DartType]; exists {
		return
	}
	for _, field := range object.Fields {
		if field.Object != nil {
			writeDartResponseObject(output, *field.Object, emitted)
		}
	}
	emitted[object.DartType] = struct{}{}
	fmt.Fprintf(output, "\nclass %s {\n", object.DartType)
	writeDartConstructor(output, object.DartType, object.Fields)
	output.WriteString("\n")
	for _, field := range object.Fields {
		fmt.Fprintf(output, "  final %s %s;\n", dartType(field), field.Name)
	}
	writeDartFromJsonFactory(output, object)
	output.WriteString("}\n")
}

func writeDartReusableObject(
	output *strings.Builder,
	object Object,
	emitted map[string]struct{},
) {
	if _, exists := emitted[object.DartType]; exists {
		return
	}
	for _, field := range object.Fields {
		if field.Object != nil {
			writeDartReusableObject(output, *field.Object, emitted)
		}
	}
	emitted[object.DartType] = struct{}{}
	fmt.Fprintf(output, "\nclass %s {\n", object.DartType)
	writeDartConstructor(output, object.DartType, object.Fields)
	output.WriteString("\n")
	for _, field := range object.Fields {
		fmt.Fprintf(output, "  final %s %s;\n", dartType(field), field.Name)
	}
	writeDartToJson(output, object.Fields)
	writeDartFromJsonFactory(output, object)
	output.WriteString("}\n")
}

func writeDartToJson(output *strings.Builder, fields []Field) {
	entries := make([]string, 0, len(fields))
	for _, field := range fields {
		entry := fmt.Sprintf("%s: %s", dartString(field.Name), dartEncodeExpression(field))
		if field.Nullable {
			entry = fmt.Sprintf("if (%s != null) %s", field.Name, entry)
		}
		entries = append(entries, entry)
	}
	inline := "  Map<String, Object?> toJson() => {" + strings.Join(entries, ", ") + "};"
	if len(inline) <= 80 {
		output.WriteString("\n" + inline + "\n")
		return
	}
	output.WriteString("\n  Map<String, Object?> toJson() => {\n")
	for _, entry := range entries {
		fmt.Fprintf(output, "    %s,\n", entry)
	}
	output.WriteString("  };\n")
}

func writeDartFromJsonFactory(output *strings.Builder, object Object) {
	arguments := make([]string, 0, len(object.Fields))
	for _, field := range object.Fields {
		arguments = append(
			arguments,
			fmt.Sprintf("%s: %s", field.Name, dartDecodeExpression("json", field)),
		)
	}
	inline := fmt.Sprintf(
		"  factory %s.fromJson(Map<String, dynamic> json) => %s(%s);",
		object.DartType,
		object.DartType,
		strings.Join(arguments, ", "),
	)
	if len(inline) <= 80 {
		output.WriteString("\n" + inline + "\n")
		return
	}
	continuation := fmt.Sprintf("      %s(%s);", object.DartType, arguments[0])
	if len(arguments) == 1 && !object.Fields[0].Array && len(continuation) <= 80 {
		fmt.Fprintf(
			output,
			"\n  factory %s.fromJson(Map<String, dynamic> json) =>\n%s\n",
			object.DartType,
			continuation,
		)
		return
	}
	fmt.Fprintf(
		output,
		"\n  factory %s.fromJson(Map<String, dynamic> json) => %s(\n",
		object.DartType,
		object.DartType,
	)
	for _, field := range object.Fields {
		writeDartDecodedArgument(output, "    ", "json", field)
	}
	output.WriteString("  );\n")
}

func writeDartDecodedArgument(
	output *strings.Builder,
	indent string,
	source string,
	field Field,
) {
	if field.Object == nil || !field.Array {
		fmt.Fprintf(
			output,
			"%s%s: %s,\n",
			indent,
			field.Name,
			dartDecodeExpression(source, field),
		)
		return
	}
	helper := "_requireObjectListField"
	if field.Nullable {
		helper = "_optionalObjectListField"
	}
	fmt.Fprintf(
		output,
		"%s%s: %s<%s>(\n",
		indent,
		field.Name,
		helper,
		field.Object.DartType,
	)
	fmt.Fprintf(output, "%s  %s,\n", indent, source)
	fmt.Fprintf(output, "%s  %s,\n", indent, dartString(field.Name))
	fmt.Fprintf(output, "%s  %s.fromJson,\n", indent, field.Object.DartType)
	fmt.Fprintf(output, "%s),\n", indent)
}

func writeDartConstructor(output *strings.Builder, dartType string, fields []Field) {
	parameters := make([]string, 0, len(fields))
	for _, field := range fields {
		parameter := "required this." + field.Name
		if field.Nullable {
			parameter = "this." + field.Name
		}
		parameters = append(parameters, parameter)
	}
	inline := fmt.Sprintf("  const %s({%s});", dartType, strings.Join(parameters, ", "))
	if len(inline) <= 80 {
		output.WriteString(inline + "\n")
		return
	}
	fmt.Fprintf(output, "  const %s({\n", dartType)
	for _, parameter := range parameters {
		fmt.Fprintf(output, "    %s,\n", parameter)
	}
	output.WriteString("  });\n")
}

func writeDartClientMethod(output *strings.Builder, method Method) {
	output.WriteString("\n  @override\n")
	if method.Stream {
		fmt.Fprintf(output, "  %s async* {\n", dartMethodSignature(method))
	} else {
		fmt.Fprintf(output, "  %s async {\n", dartMethodSignature(method))
	}
	methodConstant := "BridraMethods." + dartMethodConstant(method.Name)
	if method.Stream {
		output.WriteString("    final events = _client.stream(\n")
	} else {
		output.WriteString("    final reply = await _client.call(\n")
	}
	fmt.Fprintf(output, "      %s,\n", methodConstant)
	if method.Params != nil {
		output.WriteString("      params: request.toJson(),\n")
	}
	if method.Stream {
		output.WriteString("      timeout: timeout,\n")
	}
	output.WriteString("      cancellationToken: cancellationToken,\n")
	output.WriteString("    );\n")
	if method.Stream {
		output.WriteString("    await for (final event in events) {\n")
		output.WriteString("      if (event is RpcStreamProgress<RpcReply>) {\n")
		fmt.Fprintf(
			output,
			"        yield RpcStreamProgress<%s>(\n",
			method.Result.DartType,
		)
		output.WriteString("          sequence: event.sequence,\n")
		output.WriteString("          meta: event.meta,\n")
		output.WriteString("          progress: event.progress,\n")
		output.WriteString("        );\n")
		output.WriteString("        continue;\n")
		output.WriteString("      }\n")
		output.WriteString(
			"      final data = event as RpcStreamData<RpcReply>;\n",
		)
		output.WriteString("      final reply = data.value;\n")
	}
	bodyIndent := "    "
	if method.Stream {
		bodyIndent = "      "
	}
	fmt.Fprintf(output, "%stry {\n", bodyIndent)
	fmt.Fprintf(
		output,
		"%s  final result = _requireMap(reply.result, %s);\n",
		bodyIndent,
		dartString(method.Name+" result"),
	)
	if method.Stream {
		fmt.Fprintf(
			output,
			"%s  final value = %s(\n",
			bodyIndent,
			method.Result.DartType,
		)
		for _, field := range method.Result.Fields {
			writeDartDecodedArgument(output, bodyIndent+"    ", "result", field)
		}
		for _, field := range method.Meta {
			writeDartDecodedArgument(output, bodyIndent+"    ", "reply.meta", field)
		}
		fmt.Fprintf(output, "%s  );\n", bodyIndent)
		fmt.Fprintf(output, "%s  yield RpcStreamData(\n", bodyIndent)
		fmt.Fprintf(output, "%s    sequence: data.sequence,\n", bodyIndent)
		fmt.Fprintf(output, "%s    meta: data.meta,\n", bodyIndent)
		fmt.Fprintf(output, "%s    value: value,\n", bodyIndent)
		fmt.Fprintf(output, "%s  );\n", bodyIndent)
	} else {
		fmt.Fprintf(
			output,
			"%s  return %s(\n",
			bodyIndent,
			method.Result.DartType,
		)
		for _, field := range method.Result.Fields {
			writeDartDecodedArgument(output, bodyIndent+"    ", "result", field)
		}
		for _, field := range method.Meta {
			writeDartDecodedArgument(output, bodyIndent+"    ", "reply.meta", field)
		}
		fmt.Fprintf(output, "%s  );\n", bodyIndent)
	}
	fmt.Fprintf(output, "%s} on BackendProtocolException {\n", bodyIndent)
	fmt.Fprintf(output, "%s  rethrow;\n", bodyIndent)
	fmt.Fprintf(output, "%s} on Object catch (error) {\n", bodyIndent)
	fmt.Fprintf(output, "%s  throw BackendProtocolException(\n", bodyIndent)
	fmt.Fprintf(
		output,
		"%s    %s,\n",
		bodyIndent,
		dartString("The "+method.Name+" response does not match the protocol."),
	)
	fmt.Fprintf(output, "%s    cause: error,\n", bodyIndent)
	fmt.Fprintf(output, "%s  );\n", bodyIndent)
	fmt.Fprintf(output, "%s}\n", bodyIndent)
	if method.Stream {
		output.WriteString("    }\n")
	}
	output.WriteString("  }\n")
}

func writeDartDecoders(output *strings.Builder, schema Schema) {
	usage := collectDartDecoderUsage(schema)
	output.WriteString("Map<String, dynamic> _requireMap(Object? value, String name) {\n")
	output.WriteString("  if (value is! Map) {\n")
	output.WriteString("    throw BackendProtocolException('$name must be an object.');\n")
	output.WriteString("  }\n")
	output.WriteString("  return Map<String, dynamic>.from(value);\n")
	output.WriteString("}\n\n")
	if usage.requiredField {
		output.WriteString("T _requireField<T>(Map<String, dynamic> data, String field) {\n")
		output.WriteString("  final value = data[field];\n")
		output.WriteString("  if (value is! T) {\n")
		output.WriteString("    throw BackendProtocolException('$field must be a ${T.toString()}.');\n")
		output.WriteString("  }\n")
		output.WriteString("  return value;\n")
		output.WriteString("}\n\n")
	}
	if usage.optionalField {
		output.WriteString("T? _optionalField<T>(Map<String, dynamic> data, String field) {\n")
		output.WriteString("  final value = data[field];\n")
		output.WriteString("  if (value == null) {\n")
		output.WriteString("    return null;\n")
		output.WriteString("  }\n")
		output.WriteString("  if (value is! T) {\n")
		output.WriteString("    throw BackendProtocolException('$field must be a ${T.toString()} or null.');\n")
		output.WriteString("  }\n")
		output.WriteString("  return value;\n")
		output.WriteString("}\n\n")
	}
	if usage.requiredDateTime {
		output.WriteString("DateTime _requireDateTimeField(Map<String, dynamic> data, String field) =>\n")
		output.WriteString("    DateTime.parse(_requireField<String>(data, field));\n\n")
	}
	if usage.optionalDateTime {
		output.WriteString("DateTime? _optionalDateTimeField(Map<String, dynamic> data, String field) {\n")
		output.WriteString("  final value = _optionalField<String>(data, field);\n")
		output.WriteString("  return value == null ? null : DateTime.parse(value);\n")
		output.WriteString("}\n\n")
	}
	if usage.requiredList {
		output.WriteString("List<T> _requireListField<T>(Map<String, dynamic> data, String field) {\n")
		output.WriteString("  final value = data[field];\n")
		output.WriteString("  if (value is! List || value.any((item) => item is! T)) {\n")
		output.WriteString("    throw BackendProtocolException('$field must be a list of ${T.toString()}.');\n")
		output.WriteString("  }\n")
		output.WriteString("  return List<T>.unmodifiable(value.cast<T>());\n")
		output.WriteString("}\n\n")
	}
	if usage.optionalList {
		output.WriteString("List<T>? _optionalListField<T>(Map<String, dynamic> data, String field) {\n")
		output.WriteString("  final value = data[field];\n")
		output.WriteString("  if (value == null) {\n")
		output.WriteString("    return null;\n")
		output.WriteString("  }\n")
		output.WriteString("  if (value is! List || value.any((item) => item is! T)) {\n")
		output.WriteString("    throw BackendProtocolException(\n")
		output.WriteString("      '$field must be a list of ${T.toString()} or null.',\n")
		output.WriteString("    );\n")
		output.WriteString("  }\n")
		output.WriteString("  return List<T>.unmodifiable(value.cast<T>());\n")
		output.WriteString("}\n\n")
	}
	if usage.requiredDateTimeList {
		output.WriteString("List<DateTime> _requireDateTimeListField(Map<String, dynamic> data, String field) =>\n")
		output.WriteString("    List<DateTime>.unmodifiable(\n")
		output.WriteString("      _requireListField<String>(data, field).map(DateTime.parse),\n")
		output.WriteString("    );\n\n")
	}
	if usage.optionalDateTimeList {
		output.WriteString("List<DateTime>? _optionalDateTimeListField(Map<String, dynamic> data, String field) {\n")
		output.WriteString("  final values = _optionalListField<String>(data, field);\n")
		output.WriteString("  return values == null\n")
		output.WriteString("      ? null\n")
		output.WriteString("      : List<DateTime>.unmodifiable(values.map(DateTime.parse));\n")
		output.WriteString("}\n\n")
	}
	if usage.requiredObject {
		output.WriteString("T _requireObjectField<T>(\n")
		output.WriteString("  Map<String, dynamic> data,\n")
		output.WriteString("  String field,\n")
		output.WriteString("  T Function(Map<String, dynamic>) decode,\n")
		output.WriteString(") => decode(_requireMap(data[field], field));\n\n")
	}
	if usage.optionalObject {
		output.WriteString("T? _optionalObjectField<T>(\n")
		output.WriteString("  Map<String, dynamic> data,\n")
		output.WriteString("  String field,\n")
		output.WriteString("  T Function(Map<String, dynamic>) decode,\n")
		output.WriteString(") {\n")
		output.WriteString("  final value = data[field];\n")
		output.WriteString("  return value == null ? null : decode(_requireMap(value, field));\n")
		output.WriteString("}\n\n")
	}
	if usage.requiredObjectList {
		output.WriteString("List<T> _requireObjectListField<T>(\n")
		output.WriteString("  Map<String, dynamic> data,\n")
		output.WriteString("  String field,\n")
		output.WriteString("  T Function(Map<String, dynamic>) decode,\n")
		output.WriteString(") {\n")
		output.WriteString("  final value = data[field];\n")
		output.WriteString("  if (value is! List) {\n")
		output.WriteString("    throw BackendProtocolException('$field must be a list of objects.');\n")
		output.WriteString("  }\n")
		output.WriteString("  return List<T>.unmodifiable(\n")
		output.WriteString("    value.map((item) => decode(_requireMap(item, field))),\n")
		output.WriteString("  );\n")
		output.WriteString("}\n\n")
	}
	if usage.optionalObjectList {
		output.WriteString("List<T>? _optionalObjectListField<T>(\n")
		output.WriteString("  Map<String, dynamic> data,\n")
		output.WriteString("  String field,\n")
		output.WriteString("  T Function(Map<String, dynamic>) decode,\n")
		output.WriteString(") {\n")
		output.WriteString("  final value = data[field];\n")
		output.WriteString("  if (value == null) {\n")
		output.WriteString("    return null;\n")
		output.WriteString("  }\n")
		output.WriteString("  if (value is! List) {\n")
		output.WriteString("    throw BackendProtocolException('$field must be a list of objects or null.');\n")
		output.WriteString("  }\n")
		output.WriteString("  return List<T>.unmodifiable(\n")
		output.WriteString("    value.map((item) => decode(_requireMap(item, field))),\n")
		output.WriteString("  );\n")
		output.WriteString("}\n\n")
	}
	if usage.requiredFile {
		output.WriteString("RpcFileReference _requireFileField(\n")
		output.WriteString("  Map<String, dynamic> data,\n")
		output.WriteString("  String field,\n")
		output.WriteString(") => RpcFileReference.fromJson(_requireMap(data[field], field));\n\n")
	}
	if usage.optionalFile {
		output.WriteString("RpcFileReference? _optionalFileField(\n")
		output.WriteString("  Map<String, dynamic> data,\n")
		output.WriteString("  String field,\n")
		output.WriteString(") {\n")
		output.WriteString("  final value = data[field];\n")
		output.WriteString(
			"  return value == null ? null : RpcFileReference.fromJson(_requireMap(value, field));\n",
		)
		output.WriteString("}\n")
	}
}

type dartDecoderUsage struct {
	requiredField        bool
	optionalField        bool
	requiredDateTime     bool
	optionalDateTime     bool
	requiredDateTimeList bool
	optionalDateTimeList bool
	requiredList         bool
	optionalList         bool
	requiredObject       bool
	optionalObject       bool
	requiredObjectList   bool
	optionalObjectList   bool
	requiredFile         bool
	optionalFile         bool
}

func collectDartDecoderUsage(schema Schema) dartDecoderUsage {
	var usage dartDecoderUsage
	for _, definition := range schema.Types {
		collectDartFieldUsage(definition.Fields, &usage)
	}
	for _, method := range schema.Methods {
		collectDartFieldUsage(method.Result.Fields, &usage)
		collectDartFieldUsage(method.Meta, &usage)
	}
	return usage
}

func collectDartFieldUsage(fields []Field, usage *dartDecoderUsage) {
	for _, field := range fields {
		switch {
		case field.Type == "file":
			if field.Nullable {
				usage.optionalFile = true
			} else {
				usage.requiredFile = true
			}
		case field.Object != nil && field.Array:
			if field.Nullable {
				usage.optionalObjectList = true
			} else {
				usage.requiredObjectList = true
			}
			collectDartFieldUsage(field.Object.Fields, usage)
		case field.Object != nil:
			if field.Nullable {
				usage.optionalObject = true
			} else {
				usage.requiredObject = true
			}
			collectDartFieldUsage(field.Object.Fields, usage)
		case field.Array:
			if field.Nullable {
				usage.optionalList = true
				usage.optionalDateTimeList = usage.optionalDateTimeList || field.Format == "date-time"
			} else {
				usage.requiredList = true
				usage.requiredDateTimeList = usage.requiredDateTimeList || field.Format == "date-time"
			}
		case field.Format == "date-time":
			if field.Nullable {
				usage.optionalDateTime = true
				usage.optionalField = true
			} else {
				usage.requiredDateTime = true
				usage.requiredField = true
			}
		case field.Nullable:
			usage.optionalField = true
		default:
			usage.requiredField = true
		}
	}
}

func writeGeneratedHeader(output *strings.Builder, prefix string) {
	fmt.Fprintf(output, "%s Code generated by `bridra generate` from schema/bridra.json. DO NOT EDIT.\n\n", prefix)
}

func appendFields(groups ...[]Field) []Field {
	var fields []Field
	for _, group := range groups {
		fields = append(fields, group...)
	}
	return fields
}

func schemaHasFileResponses(schema Schema) bool {
	for _, method := range schema.Methods {
		if fieldsHaveType(method.Result.Fields, "file") ||
			fieldsHaveType(method.Meta, "file") {
			return true
		}
	}
	return false
}

func fieldsHaveType(fields []Field, fieldType string) bool {
	for _, field := range fields {
		if field.Type == fieldType {
			return true
		}
		if field.Object != nil && fieldsHaveType(field.Object.Fields, fieldType) {
			return true
		}
	}
	return false
}

func goType(field Field) string {
	value := map[string]string{
		"string":  "string",
		"integer": "int",
		"boolean": "bool",
		"file":    "framework.FileReference",
	}[field.Type]
	if field.Object != nil {
		value = field.Object.GoType
	}
	if field.Array {
		value = "[]" + value
	}
	if field.Nullable {
		value = "*" + value
	}
	return value
}

func dartType(field Field) string {
	value := map[string]string{
		"string":  "String",
		"integer": "int",
		"boolean": "bool",
		"file":    "RpcFileReference",
	}[field.Type]
	if field.Object != nil {
		value = field.Object.DartType
	}
	if field.Format == "date-time" {
		value = "DateTime"
	}
	if field.Array {
		value = "List<" + value + ">"
	}
	if field.Nullable {
		value += "?"
	}
	return value
}

func dartDecodeExpression(source string, field Field) string {
	if field.Type == "file" {
		helper := "_requireFileField"
		if field.Nullable {
			helper = "_optionalFileField"
		}
		return fmt.Sprintf("%s(%s, %s)", helper, source, dartString(field.Name))
	}
	if field.Object != nil && field.Array {
		helper := "_requireObjectListField"
		if field.Nullable {
			helper = "_optionalObjectListField"
		}
		return fmt.Sprintf(
			"%s<%s>(%s, %s, %s.fromJson)",
			helper,
			field.Object.DartType,
			source,
			dartString(field.Name),
			field.Object.DartType,
		)
	}
	if field.Object != nil {
		helper := "_requireObjectField"
		if field.Nullable {
			helper = "_optionalObjectField"
		}
		return fmt.Sprintf(
			"%s<%s>(%s, %s, %s.fromJson)",
			helper,
			field.Object.DartType,
			source,
			dartString(field.Name),
			field.Object.DartType,
		)
	}
	if field.Array {
		if field.Format == "date-time" {
			helper := "_requireDateTimeListField"
			if field.Nullable {
				helper = "_optionalDateTimeListField"
			}
			return fmt.Sprintf("%s(%s, %s)", helper, source, dartString(field.Name))
		}
		item := field
		item.Array = false
		item.Nullable = false
		helper := "_requireListField"
		if field.Nullable {
			helper = "_optionalListField"
		}
		return fmt.Sprintf(
			"%s<%s>(%s, %s)",
			helper,
			dartType(item),
			source,
			dartString(field.Name),
		)
	}
	if field.Format == "date-time" {
		if field.Nullable {
			return fmt.Sprintf("_optionalDateTimeField(%s, %s)", source, dartString(field.Name))
		}
		return fmt.Sprintf("_requireDateTimeField(%s, %s)", source, dartString(field.Name))
	}
	if field.Nullable {
		item := field
		item.Nullable = false
		return fmt.Sprintf(
			"_optionalField<%s>(%s, %s)",
			dartType(item),
			source,
			dartString(field.Name),
		)
	}
	return fmt.Sprintf(
		"_requireField<%s>(%s, %s)",
		dartType(field),
		source,
		dartString(field.Name),
	)
}

func dartEncodeExpression(field Field) string {
	value := field.Name
	if field.Type == "file" {
		if field.Nullable {
			return value + "?.toJson()"
		}
		return value + ".toJson()"
	}
	if field.Object != nil && field.Array {
		if field.Nullable {
			return value + "?.map((item) => item.toJson()).toList(growable: false)"
		}
		return value + ".map((item) => item.toJson()).toList(growable: false)"
	}
	if field.Object != nil {
		if field.Nullable {
			return value + "?.toJson()"
		}
		return value + ".toJson()"
	}
	if field.Array && field.Format == "date-time" {
		if field.Nullable {
			return value + "?.map((item) => item.toUtc().toIso8601String()).toList(growable: false)"
		}
		return value + ".map((item) => item.toUtc().toIso8601String()).toList(growable: false)"
	}
	if field.Format == "date-time" {
		if field.Nullable {
			value += "?.toUtc().toIso8601String()"
		} else {
			value += ".toUtc().toIso8601String()"
		}
	}
	return value
}

func dartString(value string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`'`, `\'`,
		`$`, `\$`,
	)
	return "'" + replacer.Replace(value) + "'"
}

func dartMethodSignature(method Method) string {
	returnType := "Future<" + method.Result.DartType + ">"
	if method.Stream {
		returnType = "Stream<RpcStreamEvent<" + method.Result.DartType + ">>"
	}
	var inline string
	if method.Params == nil {
		if method.Stream {
			return fmt.Sprintf(
				"%s %s({\n    Duration timeout = const Duration(minutes: 5),\n    RpcCancellationToken? cancellationToken,\n  })",
				returnType,
				method.ClientName,
			)
		}
		inline = fmt.Sprintf(
			"%s %s({RpcCancellationToken? cancellationToken})",
			returnType,
			method.ClientName,
		)
		if len(inline)+2 <= 80 {
			return inline
		}
		return fmt.Sprintf(
			"%s %s({\n    RpcCancellationToken? cancellationToken,\n  })",
			returnType,
			method.ClientName,
		)
	}
	if method.Stream {
		return fmt.Sprintf(
			"%s %s(\n    %s request, {\n    Duration timeout = const Duration(minutes: 5),\n    RpcCancellationToken? cancellationToken,\n  })",
			returnType,
			method.ClientName,
			method.Params.DartType,
		)
	}
	inline = fmt.Sprintf(
		"%s %s(%s request, {RpcCancellationToken? cancellationToken})",
		returnType,
		method.ClientName,
		method.Params.DartType,
	)
	if len(inline)+2 <= 80 {
		return inline
	}
	return fmt.Sprintf(
		"%s %s(\n    %s request, {\n    RpcCancellationToken? cancellationToken,\n  })",
		returnType,
		method.ClientName,
		method.Params.DartType,
	)
}

func goMethodConstant(method string) string {
	return "Method" + identifierFromSeparated(method)
}

func goRouteGroupConstant(group string) string {
	return "RouteGroup" + identifierFromSeparated(group)
}

func goRouteActionConstant(method string) string {
	return "RouteAction" + identifierFromSeparated(method)
}

func splitRouteMethod(method string) (string, string) {
	separator := strings.LastIndex(method, ".")
	return method[:separator], method[separator+1:]
}

func dartMethodConstant(method string) string {
	name := identifierFromSeparated(method)
	if name == "" {
		return "method"
	}
	return string(unicode.ToLower(rune(name[0]))) + name[1:]
}

func goIdentifier(name string) string {
	if name == "" {
		return "Field"
	}
	return string(unicode.ToUpper(rune(name[0]))) + name[1:]
}

func identifierFromSeparated(value string) string {
	var output strings.Builder
	upper := true
	for _, character := range value {
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) {
			upper = true
			continue
		}
		if upper {
			character = unicode.ToUpper(character)
			upper = false
		}
		output.WriteRune(character)
	}
	return output.String()
}

func humanName(name string) string {
	return goIdentifier(name)
}

func characterUnit(count int) string {
	if count == 1 {
		return "character"
	}
	return "characters"
}

func lowerIdentifier(name string) string {
	if name == "" {
		return "value"
	}
	return string(unicode.ToLower(rune(name[0]))) + name[1:]
}

func objectHasValidation(object Object) bool {
	for _, field := range object.Fields {
		if field.MinLength > 0 || field.MaxLength > 0 || field.Minimum != nil ||
			field.Maximum != nil || len(field.Enum) > 0 {
			return true
		}
		if field.Object != nil && objectHasValidation(*field.Object) {
			return true
		}
	}
	return false
}

func objectHasTrim(object Object) bool {
	for _, field := range object.Fields {
		if field.Trim {
			return true
		}
		if field.Object != nil && objectHasTrim(*field.Object) {
			return true
		}
	}
	return false
}
