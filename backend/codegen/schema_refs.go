package codegen

import "fmt"

func validateReferenceCycles(definitions map[string]Object) error {
	const (
		unvisited = iota
		visiting
		visited
	)
	states := make(map[string]int, len(definitions))
	var visitDefinition func(string) error
	var visitObject func(Object) error
	visitDefinition = func(name string) error {
		switch states[name] {
		case visiting:
			return fmt.Errorf("codegen: reusable type reference cycle includes %q", name)
		case visited:
			return nil
		}
		states[name] = visiting
		if err := visitObject(definitions[name]); err != nil {
			return err
		}
		states[name] = visited
		return nil
	}
	visitObject = func(object Object) error {
		for _, field := range object.Fields {
			if field.Ref != "" {
				if err := visitDefinition(field.Ref); err != nil {
					return err
				}
			}
			if field.Object != nil {
				if err := visitObject(*field.Object); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for name := range definitions {
		if err := visitDefinition(name); err != nil {
			return err
		}
	}
	return nil
}

func (schema Schema) resolveReferences() Schema {
	definitions := make(map[string]Object, len(schema.Types))
	for _, definition := range schema.Types {
		definitions[definition.Name] = definition.Object
	}

	resolved := schema
	resolved.Types = make([]NamedObject, len(schema.Types))
	for index, definition := range schema.Types {
		resolved.Types[index] = definition
		resolved.Types[index].Object = resolveObjectReferences(definition.Object, definitions)
	}
	resolved.Methods = make([]Method, len(schema.Methods))
	for index, method := range schema.Methods {
		resolved.Methods[index] = method
		if method.Params != nil {
			params := resolveObjectReferences(*method.Params, definitions)
			resolved.Methods[index].Params = &params
		}
		resolved.Methods[index].Result = resolveObjectReferences(method.Result, definitions)
		resolved.Methods[index].Meta = resolveFieldReferences(method.Meta, definitions)
	}
	return resolved
}

func resolveObjectReferences(object Object, definitions map[string]Object) Object {
	resolved := object
	resolved.Fields = resolveFieldReferences(object.Fields, definitions)
	return resolved
}

func resolveFieldReferences(fields []Field, definitions map[string]Object) []Field {
	resolved := make([]Field, len(fields))
	for index, field := range fields {
		resolved[index] = field
		resolved[index].Enum = append([]string(nil), field.Enum...)
		switch {
		case field.Ref != "":
			object := resolveObjectReferences(definitions[field.Ref], definitions)
			resolved[index].Object = &object
		case field.Object != nil:
			object := resolveObjectReferences(*field.Object, definitions)
			resolved[index].Object = &object
		}
	}
	return resolved
}
