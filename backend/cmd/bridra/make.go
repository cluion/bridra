package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/cluion/bridra/backend/scaffold"
)

var errMakeInvalid = errors.New("make: invalid scaffold request")

type makeCommand struct{}

func (makeCommand) name() string {
	return "make"
}

func (makeCommand) summary() string {
	return "Generate an application component and its tests"
}

func (makeCommand) usage() string {
	return `Usage:
  bridra make <kind> <PascalName> [--root path] [--force]

Kinds:
  controller  Generate a request Controller and test
  service     Generate a Service interface, implementation, and test
  middleware  Generate RPC Middleware and test
  request     Generate a validated Request DTO and test
  model       Generate a domain Model and test
  response    Generate a Response DTO and JSON test
  provider    Generate a Service Provider and contract test
  test        Generate an application test

Options:
  --root path  Bridra project root (default .)
  --force      Replace every colliding scaffold file atomically`
}

func (item makeCommand) run(arguments []string, stdout, stderr io.Writer) error {
	if helpRequested(arguments) {
		fmt.Fprintln(stdout, item.usage())
		return nil
	}
	if len(arguments) < 2 || strings.HasPrefix(arguments[0], "-") {
		return fmt.Errorf("%w: usage: bridra make <kind> <PascalName> [options]", errUsage)
	}
	kind := arguments[0]
	name := arguments[1]
	flags := flag.NewFlagSet(item.name(), flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, item.usage())
	}
	root := flags.String("root", ".", "Bridra project root")
	force := flags.Bool("force", false, "replace colliding scaffold files atomically")
	if err := flags.Parse(arguments[2:]); err != nil {
		return fmt.Errorf("%w: make: %v", errUsage, err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%w: make: unexpected arguments: %v", errUsage, flags.Args())
	}

	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		return fmt.Errorf("make: resolve project root: %w", err)
	}
	absoluteRoot = filepath.Clean(absoluteRoot)
	metadata, err := loadProjectMetadata(absoluteRoot)
	if err != nil {
		return fmt.Errorf("%w: %w", errMakeInvalid, err)
	}
	results, err := scaffold.Generate(scaffold.Config{
		Root: absoluteRoot, Kind: kind, Name: name,
		FrameworkModule: metadata.FrameworkModule,
		Force:           *force,
	})
	if err != nil {
		return fmt.Errorf("make: %w", err)
	}
	for _, result := range results {
		action := "Created"
		if result.Replaced {
			action = "Updated"
		}
		fmt.Fprintf(stdout, "%s %s\n", action, result.Path)
	}
	return nil
}
