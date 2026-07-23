package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const configurationSchemaVersion = 1

var errCoverageBelowMinimum = errors.New("coverage is below the configured minimum")

type configuration struct {
	SchemaVersion int                  `json:"schemaVersion"`
	GoProfile     string               `json:"goProfile"`
	GoPackages    []goPackageThreshold `json:"goPackages"`
	LCOVProfiles  []lcovThreshold      `json:"lcovProfiles"`
}

type goPackageThreshold struct {
	Name    string  `json:"name"`
	Package string  `json:"package"`
	Minimum float64 `json:"minimum"`
}

type lcovThreshold struct {
	Name    string  `json:"name"`
	Profile string  `json:"profile"`
	Minimum float64 `json:"minimum"`
}

type statementCoverage struct {
	Covered int64
	Total   int64
}

type coverageResult struct {
	Name       string
	Actual     float64
	Minimum    float64
	Covered    int64
	Total      int64
	Successful bool
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("coveragecheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root")
	configPath := flags.String("config", "tool/coverage_thresholds.json", "coverage threshold configuration")
	outputPath := flags.String("output", "coverage/summary.md", "Markdown summary output")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("coverage check: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("coverage check: unexpected arguments: %v", flags.Args())
	}

	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		return fmt.Errorf("coverage check: resolve root: %w", err)
	}
	config, err := loadConfiguration(filepath.Join(absoluteRoot, filepath.FromSlash(*configPath)))
	if err != nil {
		return err
	}
	results, err := evaluateCoverage(absoluteRoot, config)
	if err != nil {
		return err
	}
	summary := renderSummary(results)
	if _, err := io.WriteString(stdout, summary); err != nil {
		return fmt.Errorf("coverage check: write summary: %w", err)
	}
	resolvedOutput := filepath.Join(absoluteRoot, filepath.FromSlash(*outputPath))
	if err := os.MkdirAll(filepath.Dir(resolvedOutput), 0o755); err != nil {
		return fmt.Errorf("coverage check: create output directory: %w", err)
	}
	if err := os.WriteFile(resolvedOutput, []byte(summary), 0o644); err != nil {
		return fmt.Errorf("coverage check: write %s: %w", resolvedOutput, err)
	}
	for _, result := range results {
		if !result.Successful {
			return fmt.Errorf(
				"%w: %s is %.2f%%, minimum %.2f%%",
				errCoverageBelowMinimum,
				result.Name,
				result.Actual,
				result.Minimum,
			)
		}
	}
	return nil
}

func loadConfiguration(path string) (configuration, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return configuration{}, fmt.Errorf("coverage check: read configuration: %w", err)
	}
	var config configuration
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return configuration{}, fmt.Errorf("coverage check: decode configuration: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return configuration{}, errors.New("coverage check: configuration must contain one JSON object")
	}
	if err := validateConfiguration(config); err != nil {
		return configuration{}, err
	}
	return config, nil
}

func validateConfiguration(config configuration) error {
	if config.SchemaVersion != configurationSchemaVersion {
		return fmt.Errorf("coverage check: unsupported configuration schema %d", config.SchemaVersion)
	}
	if strings.TrimSpace(config.GoProfile) == "" || len(config.GoPackages) == 0 ||
		len(config.LCOVProfiles) == 0 {
		return errors.New("coverage check: Go and LCOV profiles with thresholds are required")
	}
	seen := map[string]struct{}{}
	for _, threshold := range config.GoPackages {
		if err := validateThreshold(threshold.Name, threshold.Package, threshold.Minimum); err != nil {
			return err
		}
		if _, exists := seen["go:"+threshold.Package]; exists {
			return fmt.Errorf("coverage check: duplicate Go package %s", threshold.Package)
		}
		seen["go:"+threshold.Package] = struct{}{}
	}
	for _, threshold := range config.LCOVProfiles {
		if err := validateThreshold(threshold.Name, threshold.Profile, threshold.Minimum); err != nil {
			return err
		}
		if _, exists := seen["lcov:"+threshold.Name]; exists {
			return fmt.Errorf("coverage check: duplicate LCOV name %s", threshold.Name)
		}
		seen["lcov:"+threshold.Name] = struct{}{}
	}
	return nil
}

func validateThreshold(name, identity string, minimum float64) error {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(identity) == "" {
		return errors.New("coverage check: threshold names and identities are required")
	}
	if minimum < 0 || minimum > 100 {
		return fmt.Errorf("coverage check: %s minimum must be between 0 and 100", name)
	}
	return nil
}

func evaluateCoverage(root string, config configuration) ([]coverageResult, error) {
	goCoverage, err := parseGoProfile(filepath.Join(root, filepath.FromSlash(config.GoProfile)))
	if err != nil {
		return nil, err
	}
	results := make([]coverageResult, 0, len(config.GoPackages)+len(config.LCOVProfiles))
	for _, threshold := range config.GoPackages {
		coverage, exists := goCoverage[threshold.Package]
		if !exists || coverage.Total == 0 {
			return nil, fmt.Errorf("coverage check: Go profile has no statements for %s", threshold.Package)
		}
		results = append(results, newCoverageResult(
			threshold.Name,
			threshold.Minimum,
			coverage,
		))
	}
	for _, threshold := range config.LCOVProfiles {
		coverage, err := parseLCOV(filepath.Join(root, filepath.FromSlash(threshold.Profile)))
		if err != nil {
			return nil, err
		}
		results = append(results, newCoverageResult(
			threshold.Name,
			threshold.Minimum,
			coverage,
		))
	}
	return results, nil
}

func newCoverageResult(name string, minimum float64, coverage statementCoverage) coverageResult {
	actual := float64(coverage.Covered) * 100 / float64(coverage.Total)
	return coverageResult{
		Name: name, Actual: actual, Minimum: minimum,
		Covered: coverage.Covered, Total: coverage.Total,
		Successful: actual+0.0000001 >= minimum,
	}
}

func parseGoProfile(path string) (map[string]statementCoverage, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("coverage check: open Go profile: %w", err)
	}
	defer file.Close()

	coverage := map[string]statementCoverage{}
	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if lineNumber == 1 {
			if !strings.HasPrefix(line, "mode: ") {
				return nil, errors.New("coverage check: Go profile has no mode header")
			}
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, fmt.Errorf("coverage check: malformed Go profile line %d", lineNumber)
		}
		location := fields[0]
		colon := strings.LastIndex(location, ":")
		if colon < 1 {
			return nil, fmt.Errorf("coverage check: malformed Go location on line %d", lineNumber)
		}
		statements, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || statements < 0 {
			return nil, fmt.Errorf("coverage check: invalid statement count on line %d", lineNumber)
		}
		count, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || count < 0 {
			return nil, fmt.Errorf("coverage check: invalid execution count on line %d", lineNumber)
		}
		packagePath := filepath.ToSlash(filepath.Dir(location[:colon]))
		entry := coverage[packagePath]
		entry.Total += statements
		if count > 0 {
			entry.Covered += statements
		}
		coverage[packagePath] = entry
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("coverage check: scan Go profile: %w", err)
	}
	if lineNumber == 0 {
		return nil, errors.New("coverage check: Go profile is empty")
	}
	return coverage, nil
}

func parseLCOV(path string) (statementCoverage, error) {
	file, err := os.Open(path)
	if err != nil {
		return statementCoverage{}, fmt.Errorf("coverage check: open LCOV profile %s: %w", path, err)
	}
	defer file.Close()

	var coverage statementCoverage
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "LF:"):
			value, err := parseLCOVCount(line[3:])
			if err != nil {
				return statementCoverage{}, fmt.Errorf("coverage check: invalid LF in %s: %w", path, err)
			}
			coverage.Total += value
		case strings.HasPrefix(line, "LH:"):
			value, err := parseLCOVCount(line[3:])
			if err != nil {
				return statementCoverage{}, fmt.Errorf("coverage check: invalid LH in %s: %w", path, err)
			}
			coverage.Covered += value
		}
	}
	if err := scanner.Err(); err != nil {
		return statementCoverage{}, fmt.Errorf("coverage check: scan LCOV profile %s: %w", path, err)
	}
	if coverage.Total == 0 || coverage.Covered > coverage.Total {
		return statementCoverage{}, fmt.Errorf("coverage check: LCOV profile %s has invalid line totals", path)
	}
	return coverage, nil
}

func parseLCOVCount(value string) (int64, error) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed < 0 {
		return 0, errors.New("count must be a non-negative integer")
	}
	return parsed, nil
}

func renderSummary(results []coverageResult) string {
	var output strings.Builder
	output.WriteString("# Bridra coverage\n\n")
	output.WriteString("| Surface | Actual | Minimum | Statements/lines | Status |\n")
	output.WriteString("| --- | ---: | ---: | ---: | --- |\n")
	for _, result := range results {
		status := "pass"
		if !result.Successful {
			status = "fail"
		}
		fmt.Fprintf(
			&output,
			"| %s | %.2f%% | %.2f%% | %d/%d | %s |\n",
			result.Name,
			result.Actual,
			result.Minimum,
			result.Covered,
			result.Total,
			status,
		)
	}
	return output.String()
}
