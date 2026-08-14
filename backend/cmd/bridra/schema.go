package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/cluion/bridra/backend/codegen"
)

var errSchemaIncompatible = errors.New("schema: incompatible RPC contract")

type schemaCommand struct{}

type schemaCheckIdentity struct {
	Path            string `json:"path"`
	ProtocolVersion int    `json:"protocolVersion"`
}

type schemaCheckJSONReport struct {
	ReportVersion        int                               `json:"reportVersion"`
	Status               codegen.SchemaCompatibilityStatus `json:"status"`
	Baseline             schemaCheckIdentity               `json:"baseline"`
	Current              schemaCheckIdentity               `json:"current"`
	ProtocolBumpRequired bool                              `json:"protocolBumpRequired"`
	ProtocolBumpPresent  bool                              `json:"protocolBumpPresent"`
	MinimumProtocol      int                               `json:"minimumProtocolVersion"`
	BreakingChanges      int                               `json:"breakingChanges"`
	Changes              []codegen.SchemaChange            `json:"changes"`
}

func (schemaCommand) name() string {
	return "schema"
}

func (schemaCommand) summary() string {
	return "Check RPC schema compatibility and protocol versioning"
}

func (schemaCommand) usage() string {
	return `Usage:
  bridra schema check --against path [options]

Commands:
  check  Compare the current RPC wire schema with a baseline schema

Options:
  --against path  Baseline Bridra schema to compare against (required)
  --schema path   Current Bridra schema (default schema/bridra.json)
  --json          Write a versioned machine-readable report

Breaking wire changes require a protocolVersion greater than the baseline.
Generated Go and Dart identifier renames are source migrations and are not
classified as wire changes by this command.`
}

func (item schemaCommand) run(arguments []string, stdout, stderr io.Writer) error {
	if helpRequested(arguments) {
		fmt.Fprintln(stdout, item.usage())
		return nil
	}
	if len(arguments) == 0 || arguments[0] != "check" {
		return fmt.Errorf("%w: usage: bridra schema check --against path [options]", errUsage)
	}

	flags := flag.NewFlagSet("schema check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, item.usage())
	}
	baselinePath := flags.String("against", "", "baseline Bridra schema")
	currentPath := flags.String("schema", "schema/bridra.json", "current Bridra schema")
	jsonOutput := flags.Bool("json", false, "write a machine-readable report")
	if err := flags.Parse(arguments[1:]); err != nil {
		return fmt.Errorf("%w: schema check: %v", errUsage, err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf(
			"%w: schema check: unexpected arguments: %v",
			errUsage,
			flags.Args(),
		)
	}
	if strings.TrimSpace(*baselinePath) == "" {
		return fmt.Errorf("%w: schema check: --against is required", errUsage)
	}

	baseline, err := codegen.LoadSchema(*baselinePath)
	if err != nil {
		return fmt.Errorf("schema check: load baseline: %w", err)
	}
	current, err := codegen.LoadSchema(*currentPath)
	if err != nil {
		return fmt.Errorf("schema check: load current: %w", err)
	}
	report, err := codegen.CompareSchemas(baseline, current)
	if err != nil {
		return fmt.Errorf("schema check: %w", err)
	}

	if *jsonOutput {
		output := schemaCheckJSONReport{
			ReportVersion: 1,
			Status:        report.Status,
			Baseline: schemaCheckIdentity{
				Path:            *baselinePath,
				ProtocolVersion: baseline.ProtocolVersion,
			},
			Current: schemaCheckIdentity{
				Path:            *currentPath,
				ProtocolVersion: current.ProtocolVersion,
			},
			ProtocolBumpRequired: report.ProtocolBumpRequired,
			ProtocolBumpPresent:  report.ProtocolBumpPresent,
			MinimumProtocol:      report.MinimumProtocolVersion,
			BreakingChanges:      report.BreakingChanges,
			Changes:              report.Changes,
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(output); err != nil {
			return fmt.Errorf("schema check: encode report: %w", err)
		}
	} else {
		writeSchemaCheckReport(stdout, *baselinePath, *currentPath, report)
	}

	if report.Status == codegen.SchemaIncompatible {
		return fmt.Errorf(
			"%w: current protocol %d; require at least %d",
			errSchemaIncompatible,
			report.CurrentProtocolVersion,
			report.MinimumProtocolVersion,
		)
	}
	return nil
}

func writeSchemaCheckReport(
	output io.Writer,
	baselinePath, currentPath string,
	report codegen.SchemaCompatibilityReport,
) {
	fmt.Fprintln(output, "Bridra Schema Check")
	fmt.Fprintf(output, "Status: %s\n", report.Status)
	fmt.Fprintf(
		output,
		"Baseline: protocol %d (%s)\n",
		report.BaselineProtocolVersion,
		baselinePath,
	)
	fmt.Fprintf(
		output,
		"Current: protocol %d (%s)\n",
		report.CurrentProtocolVersion,
		currentPath,
	)
	fmt.Fprintf(output, "Breaking wire changes: %d\n", report.BreakingChanges)
	if len(report.Changes) == 0 {
		fmt.Fprintln(output, "Changes: none")
	} else {
		fmt.Fprintln(output, "Changes:")
		for _, change := range report.Changes {
			classification := "compatible"
			if change.Breaking {
				classification = "breaking"
			}
			fmt.Fprintf(
				output,
				"  - [%s] %s %s: %s\n",
				classification,
				change.Code,
				change.Path,
				change.Message,
			)
		}
	}
	if report.Status == codegen.SchemaVersionedBreak {
		fmt.Fprintln(output, "Protocol bump isolates the breaking wire changes.")
	}
	if report.Status == codegen.SchemaIncompatible {
		fmt.Fprintf(
			output,
			"Required protocolVersion: %d or newer\n",
			report.MinimumProtocolVersion,
		)
	}
}
