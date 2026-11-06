package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/cluion/bridra/backend/codegen"
)

var generatedGoProtocolPattern = regexp.MustCompile(
	`(?m)^const ProtocolVersion = ([1-9][0-9]*)\r?$`,
)

var generatedDartProtocolPattern = regexp.MustCompile(
	`(?m)^const supportedBackendProtocolVersion = ([1-9][0-9]*);\r?$`,
)

var generatedTypeScriptProtocolPattern = regexp.MustCompile(
	`(?m)^export const protocolVersion = ([1-9][0-9]*);\r?$`,
)

func verifyApplicationRPCContract(root string, metadata projectMetadata) error {
	schema, err := codegen.LoadSchema(filepath.Join(root, "schema", "bridra.json"))
	if err != nil {
		return err
	}
	goVersion, err := loadGeneratedProtocolVersion(
		filepath.Join(root, filepath.FromSlash(codegen.GoProtocolPath)),
		"Go",
		generatedGoProtocolPattern,
	)
	if err != nil {
		return err
	}
	clientPath, clientLanguage, pattern := codegen.DartClientPath, "Dart", generatedDartProtocolPattern
	if metadata.isWebUI() {
		clientPath, clientLanguage, pattern = codegen.TypeScriptClientPath, "TypeScript", generatedTypeScriptProtocolPattern
	}
	clientVersion, err := loadGeneratedProtocolVersion(
		filepath.Join(root, filepath.FromSlash(clientPath)),
		clientLanguage,
		pattern,
	)
	if err != nil {
		return err
	}
	if metadata.ProtocolVersion != schema.ProtocolVersion ||
		metadata.ProtocolVersion != goVersion ||
		metadata.ProtocolVersion != clientVersion {
		return fmt.Errorf(
			"protocol versions disagree: metadata %d, schema %d, Go %d, %s %d",
			metadata.ProtocolVersion,
			schema.ProtocolVersion,
			goVersion,
			clientLanguage, clientVersion,
		)
	}
	return nil
}

func loadGeneratedProtocolVersion(path, language string, pattern *regexp.Regexp) (int, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read generated %s protocol: %w", language, err)
	}
	matches := pattern.FindAllSubmatch(contents, -1)
	if len(matches) != 1 {
		return 0, fmt.Errorf(
			"generated %s contract must contain exactly one protocol version",
			language,
		)
	}
	version, err := strconv.Atoi(string(matches[0][1]))
	if err != nil {
		return 0, fmt.Errorf("parse generated %s protocol: %w", language, err)
	}
	return version, nil
}
