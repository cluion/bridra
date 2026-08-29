package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cluion/bridra/backend/codegen"
)

const dartFormatTimeout = 2 * time.Minute

type dartFormatter func(root string, source []byte) ([]byte, error)

type generateCommand struct {
	formatDart dartFormatter
}

func newGenerateCommand() generateCommand {
	return generateCommand{formatDart: formatDartWithFVM}
}

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
  --check                Fail when generated files are stale

Generated Dart is canonicalized with the project-pinned FVM Dart SDK.`
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
	outputs, err = item.formatDartOutputs(*root, outputs)
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

func (item generateCommand) formatDartOutputs(
	root string,
	outputs []codegen.Output,
) ([]codegen.Output, error) {
	formatter := item.formatDart
	if formatter == nil {
		formatter = formatDartWithFVM
	}
	for index := range outputs {
		if !strings.HasSuffix(outputs[index].Path, ".dart") {
			continue
		}
		formatted, err := formatter(root, outputs[index].Content)
		if err != nil {
			return nil, fmt.Errorf("format %s: %w", outputs[index].Path, err)
		}
		outputs[index].Content = formatted
	}
	return outputs, nil
}

func formatDartWithFVM(root string, source []byte) (formatted []byte, resultErr error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}
	temporary, err := os.CreateTemp(absoluteRoot, ".bridra-dart-format-*.dart")
	if err != nil {
		return nil, fmt.Errorf("create temporary Dart source: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		if removeErr := os.Remove(temporaryPath); removeErr != nil && !os.IsNotExist(removeErr) {
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("remove temporary Dart source: %w", removeErr),
			)
		}
	}()
	if _, err := temporary.Write(source); err != nil {
		_ = temporary.Close()
		return nil, fmt.Errorf("write temporary Dart source: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return nil, fmt.Errorf("close temporary Dart source: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dartFormatTimeout)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		"fvm",
		"dart",
		"format",
		"--output=write",
		temporaryPath,
	)
	command.Dir = absoluteRoot
	output, err := command.CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("project-pinned Dart formatter timed out after %s", dartFormatTimeout)
		}
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			return nil, fmt.Errorf("run project-pinned Dart formatter: %w", err)
		}
		return nil, fmt.Errorf("run project-pinned Dart formatter: %w: %s", err, detail)
	}
	formatted, err = os.ReadFile(temporaryPath)
	if err != nil {
		return nil, fmt.Errorf("read formatted Dart source: %w", err)
	}
	return formatted, nil
}

func helpRequested(arguments []string) bool {
	for _, argument := range arguments {
		if argument == "-h" || argument == "--help" {
			return true
		}
	}
	return false
}
