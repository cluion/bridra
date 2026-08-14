package codegen

import (
	"fmt"
	"slices"
)

type SchemaCompatibilityStatus string

const (
	SchemaCompatible     SchemaCompatibilityStatus = "compatible"
	SchemaVersionedBreak SchemaCompatibilityStatus = "versioned_break"
	SchemaIncompatible   SchemaCompatibilityStatus = "incompatible"
)

type SchemaChange struct {
	Code     string `json:"code"`
	Path     string `json:"path"`
	Breaking bool   `json:"breaking"`
	Message  string `json:"message"`
}

type SchemaCompatibilityReport struct {
	Status                  SchemaCompatibilityStatus `json:"status"`
	BaselineProtocolVersion int                       `json:"baselineProtocolVersion"`
	CurrentProtocolVersion  int                       `json:"currentProtocolVersion"`
	ProtocolBumpRequired    bool                      `json:"protocolBumpRequired"`
	ProtocolBumpPresent     bool                      `json:"protocolBumpPresent"`
	MinimumProtocolVersion  int                       `json:"minimumProtocolVersion"`
	BreakingChanges         int                       `json:"breakingChanges"`
	Changes                 []SchemaChange            `json:"changes"`
}

// CompareSchemas compares the wire contract represented by two valid Bridra
// schemas. Generated Go and Dart identifier changes are source migrations and
// are deliberately outside this wire-level comparison.
func CompareSchemas(baseline, current Schema) (SchemaCompatibilityReport, error) {
	if err := baseline.Validate(); err != nil {
		return SchemaCompatibilityReport{}, fmt.Errorf(
			"codegen: validate baseline schema: %w",
			err,
		)
	}
	if err := current.Validate(); err != nil {
		return SchemaCompatibilityReport{}, fmt.Errorf(
			"codegen: validate current schema: %w",
			err,
		)
	}

	changes := make([]SchemaChange, 0)
	baselineMethods := methodsByName(baseline.Methods)
	currentMethods := methodsByName(current.Methods)
	for _, baselineMethod := range baseline.Methods {
		currentMethod, exists := currentMethods[baselineMethod.Name]
		path := "methods[" + baselineMethod.Name + "]"
		if !exists {
			changes = append(changes, breakingChange(
				"method_removed",
				path,
				fmt.Sprintf("RPC method %q was removed.", baselineMethod.Name),
			))
			continue
		}
		compareMethod(path, baselineMethod, currentMethod, &changes)
	}
	for _, currentMethod := range current.Methods {
		if _, exists := baselineMethods[currentMethod.Name]; exists {
			continue
		}
		changes = append(changes, compatibleChange(
			"method_added",
			"methods["+currentMethod.Name+"]",
			fmt.Sprintf("RPC method %q was added.", currentMethod.Name),
		))
	}

	breakingChanges := 0
	for _, change := range changes {
		if change.Breaking {
			breakingChanges++
		}
	}
	protocolBumpRequired := breakingChanges > 0
	minimumProtocolVersion := baseline.ProtocolVersion
	if protocolBumpRequired {
		minimumProtocolVersion++
	}
	protocolBumpPresent := current.ProtocolVersion > baseline.ProtocolVersion
	compatible := current.ProtocolVersion >= baseline.ProtocolVersion &&
		(!protocolBumpRequired || protocolBumpPresent)
	status := SchemaCompatible
	if !compatible {
		status = SchemaIncompatible
	} else if protocolBumpRequired {
		status = SchemaVersionedBreak
	}

	return SchemaCompatibilityReport{
		Status:                  status,
		BaselineProtocolVersion: baseline.ProtocolVersion,
		CurrentProtocolVersion:  current.ProtocolVersion,
		ProtocolBumpRequired:    protocolBumpRequired,
		ProtocolBumpPresent:     protocolBumpPresent,
		MinimumProtocolVersion:  minimumProtocolVersion,
		BreakingChanges:         breakingChanges,
		Changes:                 changes,
	}, nil
}

func methodsByName(methods []Method) map[string]Method {
	result := make(map[string]Method, len(methods))
	for _, method := range methods {
		result[method.Name] = method
	}
	return result
}

func compareMethod(path string, baseline, current Method, changes *[]SchemaChange) {
	if baseline.Stream != current.Stream {
		*changes = append(*changes, breakingChange(
			"streaming_changed",
			path+".stream",
			"The RPC method changed between unary and server streaming.",
		))
	}
	switch {
	case baseline.Params == nil && current.Params != nil:
		*changes = append(*changes, breakingChange(
			"params_added",
			path+".params",
			"Request parameters were added to a method that had none.",
		))
	case baseline.Params != nil && current.Params == nil:
		*changes = append(*changes, breakingChange(
			"params_removed",
			path+".params",
			"Request parameters were removed from the method.",
		))
	case baseline.Params != nil && current.Params != nil:
		compareObject(path+".params", *baseline.Params, *current.Params, requestFields, changes)
	}
	compareObject(path+".result", baseline.Result, current.Result, responseFields, changes)
	compareFields(path+".meta", baseline.Meta, current.Meta, responseFields, changes)
}

type fieldDirection int

const (
	requestFields fieldDirection = iota
	responseFields
)

func compareObject(
	path string,
	baseline, current Object,
	direction fieldDirection,
	changes *[]SchemaChange,
) {
	compareFields(path+".fields", baseline.Fields, current.Fields, direction, changes)
}

func compareFields(
	path string,
	baseline, current []Field,
	direction fieldDirection,
	changes *[]SchemaChange,
) {
	baselineFields := fieldsByName(baseline)
	currentFields := fieldsByName(current)
	for _, baselineField := range baseline {
		fieldPath := path + "[" + baselineField.Name + "]"
		currentField, exists := currentFields[baselineField.Name]
		if !exists {
			breaking := direction == requestFields || !baselineField.Nullable
			message := fmt.Sprintf("Field %q was removed.", baselineField.Name)
			if breaking {
				*changes = append(*changes, breakingChange("field_removed", fieldPath, message))
			} else {
				*changes = append(*changes, compatibleChange(
					"nullable_response_field_removed",
					fieldPath,
					message,
				))
			}
			continue
		}
		compareField(fieldPath, baselineField, currentField, direction, changes)
	}
	for _, currentField := range current {
		if _, exists := baselineFields[currentField.Name]; exists {
			continue
		}
		fieldPath := path + "[" + currentField.Name + "]"
		breaking := direction == requestFields || !currentField.Nullable
		message := fmt.Sprintf("Field %q was added.", currentField.Name)
		if breaking {
			*changes = append(*changes, breakingChange("field_added", fieldPath, message))
		} else {
			*changes = append(*changes, compatibleChange(
				"nullable_response_field_added",
				fieldPath,
				message,
			))
		}
	}
}

func fieldsByName(fields []Field) map[string]Field {
	result := make(map[string]Field, len(fields))
	for _, field := range fields {
		result[field.Name] = field
	}
	return result
}

func compareField(
	path string,
	baseline, current Field,
	direction fieldDirection,
	changes *[]SchemaChange,
) {
	if baseline.Type != current.Type || baseline.Array != current.Array ||
		baseline.Format != current.Format {
		*changes = append(*changes, breakingChange(
			"field_shape_changed",
			path,
			"The field type, array shape, or wire format changed.",
		))
		return
	}
	if baseline.Nullable != current.Nullable {
		*changes = append(*changes, breakingChange(
			"field_nullability_changed",
			path+".nullable",
			"The field presence or nullability contract changed.",
		))
	}
	if direction == requestFields && (baseline.MinLength != current.MinLength ||
		baseline.MaxLength != current.MaxLength ||
		baseline.Trim != current.Trim ||
		!equalStringSet(baseline.Enum, current.Enum)) {
		*changes = append(*changes, breakingChange(
			"request_rules_changed",
			path,
			"The request validation or normalization rules changed.",
		))
	}
	if baseline.Type != "object" || baseline.Object == nil || current.Object == nil {
		return
	}
	compareObject(path+".object", *baseline.Object, *current.Object, direction, changes)
}

func equalStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := slices.Clone(left)
	rightCopy := slices.Clone(right)
	slices.Sort(leftCopy)
	slices.Sort(rightCopy)
	return slices.Equal(leftCopy, rightCopy)
}

func breakingChange(code, path, message string) SchemaChange {
	return SchemaChange{Code: code, Path: path, Breaking: true, Message: message}
}

func compatibleChange(code, path, message string) SchemaChange {
	return SchemaChange{Code: code, Path: path, Message: message}
}
