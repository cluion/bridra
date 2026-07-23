package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/cluion/bridra/backend/internal/releaseinfo"
)

type versionCommand struct {
	metadata func() releaseinfo.Metadata
}

func newVersionCommand() versionCommand {
	return versionCommand{metadata: releaseinfo.Current}
}

func (versionCommand) name() string {
	return "version"
}

func (versionCommand) summary() string {
	return "Show CLI, framework, template, protocol, and build metadata"
}

func (versionCommand) usage() string {
	return `Usage:
  bridra version [--json]

Options:
  --json  Write machine-readable release metadata`
}

func (item versionCommand) run(arguments []string, stdout, stderr io.Writer) error {
	if helpRequested(arguments) {
		fmt.Fprintln(stdout, item.usage())
		return nil
	}
	flags := flag.NewFlagSet(item.name(), flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, item.usage())
	}
	jsonOutput := flags.Bool("json", false, "write JSON metadata")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("%w: version: %v", errUsage, err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%w: version: unexpected arguments: %v", errUsage, flags.Args())
	}
	metadata := item.metadata()
	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(metadata); err != nil {
			return fmt.Errorf("version: encode metadata: %w", err)
		}
		return nil
	}
	fmt.Fprintf(stdout, "Bridra CLI %s\n", metadata.CLIVersion)
	fmt.Fprintf(stdout, "Framework: %s\n", metadata.FrameworkVersion)
	fmt.Fprintf(stdout, "Template: %d\n", metadata.TemplateVersion)
	fmt.Fprintf(stdout, "Protocol: %d\n", metadata.ProtocolVersion)
	fmt.Fprintf(stdout, "Commit: %s\n", metadata.Commit)
	fmt.Fprintf(stdout, "Built: %s\n", metadata.BuildDate)
	fmt.Fprintf(stdout, "Go: %s\n", metadata.GoVersion)
	fmt.Fprintf(stdout, "Target: %s\n", metadata.Target)
	fmt.Fprintf(stdout, "Install: go install %s@v%s\n", metadata.CLIInstallPath, metadata.CLIVersion)
	fmt.Fprintf(stdout, "Flutter: %s %s\n", metadata.FlutterPackage, metadata.FlutterConstraint)
	return nil
}
