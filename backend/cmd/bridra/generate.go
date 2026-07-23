package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/cluion/bridra/backend/codegen"
)

type generateCommand struct{}

func (generateCommand) name() string {
	return "generate"
}

func (generateCommand) summary() string {
	return "Generate Go and Dart APIs from a Bridra schema"
}

func (generateCommand) usage() string {
	return `Usage:
  bridra generate [options]

Options:
  --schema path          Path to the Bridra schema (default schema/bridra.json)
  --root path            Project root for generated outputs (default .)
  --framework-import     Go import for the Bridra framework package
  --dart-runtime-import  Dart import URI for the Bridra runtime package
  --check                Fail when generated files are stale`
}

func (item generateCommand) run(arguments []string, stdout, stderr io.Writer) error {
	if helpRequested(arguments) {
		fmt.Fprintln(stdout, item.usage())
		return nil
	}

	flags := flag.NewFlagSet(item.name(), flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, item.usage())
	}
	schemaPath := flags.String("schema", "schema/bridra.json", "path to the Bridra schema")
	root := flags.String("root", ".", "project root for generated outputs")
	check := flags.Bool("check", false, "fail when generated files are stale")
	frameworkImport := flags.String(
		"framework-import",
		codegen.DefaultGoFrameworkImport,
		"Go import for the Bridra framework package",
	)
	dartRuntimeImport := flags.String(
		"dart-runtime-import",
		codegen.DefaultDartRuntimeImport,
		"Dart import URI for the Bridra runtime package",
	)
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("%w: generate: %v", errUsage, err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%w: generate: unexpected arguments: %v", errUsage, flags.Args())
	}

	schema, err := codegen.LoadSchema(*schemaPath)
	if err != nil {
		return fmt.Errorf("generate: %w", err)
	}
	outputs, err := codegen.GenerateWithOptions(schema, codegen.Options{
		GoFrameworkImport: *frameworkImport,
		DartRuntimeImport: *dartRuntimeImport,
	})
	if err != nil {
		return fmt.Errorf("generate: %w", err)
	}
	if *check {
		if err := codegen.Check(*root, outputs); err != nil {
			return fmt.Errorf("generate: %w", err)
		}
		fmt.Fprintln(stdout, "Bridra generated files are up to date.")
		return nil
	}
	if err := codegen.Write(*root, outputs); err != nil {
		return fmt.Errorf("generate: %w", err)
	}
	for _, output := range outputs {
		fmt.Fprintf(stdout, "Generated %s\n", output.Path)
	}
	return nil
}

func helpRequested(arguments []string) bool {
	for _, argument := range arguments {
		if argument == "-h" || argument == "--help" {
			return true
		}
	}
	return false
}
