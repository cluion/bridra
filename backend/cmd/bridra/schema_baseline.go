package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cluion/bridra/backend/codegen"
)

func (item schemaCommand) updateBaseline(arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("schema baseline", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprintln(stderr, item.usage()) }
	update := flags.Bool("update", false, "explicitly replace the application baseline")
	schemaPath := flags.String("schema", "schema/bridra.json", "current Bridra schema")
	baselinePath := flags.String("baseline", "schema/bridra.baseline.json", "application baseline")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("%w: schema baseline: %v", errUsage, err)
	}
	if !*update || flags.NArg() != 0 ||
		strings.TrimSpace(*schemaPath) == "" || strings.TrimSpace(*baselinePath) == "" {
		return fmt.Errorf("%w: usage: bridra schema baseline --update [--schema path] [--baseline path]", errUsage)
	}
	protocol, err := updateSchemaBaseline(*schemaPath, *baselinePath)
	if err != nil {
		return fmt.Errorf("schema baseline: %w", err)
	}
	fmt.Fprintf(stdout, "Updated baseline %s from %s (protocolVersion %d).\nReview the baseline diff before committing.\n", *baselinePath, *schemaPath, protocol)
	return nil
}

func updateSchemaBaseline(schemaPath, baselinePath string) (int, error) {
	source, err := os.Stat(schemaPath)
	if err != nil {
		return 0, err
	}
	if !source.Mode().IsRegular() {
		return 0, fmt.Errorf("current schema must be a regular file")
	}
	mode := os.FileMode(0o644)
	baseline, err := os.Lstat(baselinePath)
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	if err == nil {
		if !baseline.Mode().IsRegular() {
			return 0, fmt.Errorf("baseline must be a regular file, not a symlink or directory")
		}
		if os.SameFile(source, baseline) {
			return 0, fmt.Errorf("current schema and baseline must be different files")
		}
		mode = baseline.Mode().Perm()
	}
	content, err := os.ReadFile(schemaPath)
	if err != nil {
		return 0, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(baselinePath), ".bridra-baseline-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	if _, err := temporary.Write(content); err != nil {
		return 0, err
	}
	// Validate the exact snapshot being published, even if the source changes.
	schema, err := codegen.LoadSchema(temporary.Name())
	if err != nil {
		return 0, err
	}
	if err := temporary.Chmod(mode); err != nil {
		return 0, err
	}
	if err := temporary.Sync(); err != nil {
		return 0, err
	}
	if err := temporary.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(temporary.Name(), baselinePath); err != nil {
		return 0, err
	}
	return schema.ProtocolVersion, nil
}
