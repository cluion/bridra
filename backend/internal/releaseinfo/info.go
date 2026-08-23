package releaseinfo

import (
	"runtime"

	"github.com/cluion/bridra/backend/framework"
)

const (
	SchemaVersion             = 1
	ProjectMetadataVersion    = 2
	ProjectTemplateVersion    = 4
	GoModule                  = "github.com/cluion/bridra/backend"
	CLIInstallPath            = GoModule + "/cmd/bridra"
	FlutterPackage            = "bridra_flutter"
	RecommendedFlutterVersion = "3.44.6"
)

var (
	Version   = framework.FrameworkVersion
	Commit    = "development"
	BuildDate = "unknown"
)

type Metadata struct {
	SchemaVersion     int    `json:"schemaVersion"`
	CLIVersion        string `json:"cliVersion"`
	FrameworkVersion  string `json:"frameworkVersion"`
	TemplateVersion   int    `json:"templateVersion"`
	ProtocolVersion   int    `json:"protocolVersion"`
	Commit            string `json:"commit"`
	BuildDate         string `json:"buildDate"`
	GoVersion         string `json:"goVersion"`
	Target            string `json:"target"`
	GoModule          string `json:"goModule"`
	CLIInstallPath    string `json:"cliInstallPath"`
	FlutterPackage    string `json:"flutterPackage"`
	FlutterConstraint string `json:"flutterConstraint"`
}

func Current() Metadata {
	return Metadata{
		SchemaVersion:     SchemaVersion,
		CLIVersion:        Version,
		FrameworkVersion:  framework.FrameworkVersion,
		TemplateVersion:   ProjectTemplateVersion,
		ProtocolVersion:   framework.ProtocolVersion,
		Commit:            Commit,
		BuildDate:         BuildDate,
		GoVersion:         runtime.Version(),
		Target:            runtime.GOOS + "/" + runtime.GOARCH,
		GoModule:          GoModule,
		CLIInstallPath:    CLIInstallPath,
		FlutterPackage:    FlutterPackage,
		FlutterConstraint: FlutterConstraint(),
	}
}

func GoModuleVersion() string {
	return "v" + Version
}

func FlutterConstraint() string {
	return "^" + Version
}
