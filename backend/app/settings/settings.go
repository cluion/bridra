package settings

import (
	"io"

	"github.com/cluion/bridra/backend/framework"
)

var (
	BackendToken = framework.NewSecretConfigKey("backend.token", "")
	LogOutput    = framework.NewConfigKey("logging.output", io.Writer(io.Discard))
	RuntimeName  = framework.NewConfigKey("runtime.name", "Go backend")
	loader       = framework.NewConfigLoader(
		framework.StringConfig(BackendToken, framework.RequiredString("is required")),
		framework.TypedConfig(LogOutput),
		framework.StringConfig(RuntimeName),
	)
)

func New(token string, logs io.Writer, runtime string) (*framework.Config, error) {
	values := map[string]any{BackendToken.Name(): token}
	if logs != nil {
		values[LogOutput.Name()] = logs
	}
	if runtime != "" {
		values[RuntimeName.Name()] = runtime
	}
	return Load(framework.NewMapConfigSource("runtime", values))
}

func Load(sources ...framework.ConfigSource) (*framework.Config, error) {
	return loader.Load(sources...)
}
