package codegen

import "fmt"

// Check collisions only where an instance method is actually generated.
func validateGeneratedMembers(schema Schema) error {
	for _, definition := range schema.Types {
		if err := validateDartEncoderMembers(definition.Object); err != nil {
			return err
		}
	}
	for _, method := range schema.Methods {
		if method.Params != nil {
			if err := validateGoRequestMembers(*method.Params); err != nil {
				return err
			}
			if err := validateDartEncoderMembers(*method.Params); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateGoRequestMembers(object Object) error {
	for _, field := range object.Fields {
		name := goIdentifier(field.Name)
		if name == "ValidatePayload" ||
			(name == "Normalize" && objectHasTrim(object)) ||
			(name == "Validate" && objectHasValidation(object)) {
			return fmt.Errorf("codegen: field %q in %s conflicts with generated Go method %q", field.Name, object.GoType, name)
		}
		if field.Object != nil {
			if err := validateGoRequestMembers(*field.Object); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateDartEncoderMembers(object Object) error {
	for _, field := range object.Fields {
		name := dartFieldIdentifier(field.Name)
		if name == "toJson" {
			return fmt.Errorf("codegen: field %q in %s conflicts with generated Dart member %q", field.Name, object.DartType, name)
		}
		if field.Object != nil {
			if err := validateDartEncoderMembers(*field.Object); err != nil {
				return err
			}
		}
	}
	return nil
}
