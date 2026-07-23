package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/cluion/bridra/backend/app/providers"
	"github.com/cluion/bridra/backend/app/settings"
	"github.com/cluion/bridra/backend/framework"
)

type Config struct {
	Token   string
	Logs    io.Writer
	Runtime string
}

func NewRouter(token string, logs io.Writer) *framework.Router {
	return New(Config{Token: token, Logs: logs, Runtime: "Go sidecar"})
}

func New(config Config) *framework.Router {
	application, err := Build(config)
	if err != nil {
		panic(fmt.Errorf("app: build: %w", err))
	}
	return application.Router()
}

func Build(config Config, additionalProviders ...framework.ServiceProvider) (*framework.Application, error) {
	values := map[string]any{settings.BackendToken.Name(): config.Token}
	if config.Logs != nil {
		values[settings.LogOutput.Name()] = config.Logs
	}
	if config.Runtime != "" {
		values[settings.RuntimeName.Name()] = config.Runtime
	}
	return BuildFromSources(
		[]framework.ConfigSource{framework.NewMapConfigSource("runtime", values)},
		additionalProviders...,
	)
}

func BuildFromSources(
	sources []framework.ConfigSource,
	additionalProviders ...framework.ServiceProvider,
) (*framework.Application, error) {
	applicationConfig, err := settings.Load(sources...)
	if err != nil {
		return nil, fmt.Errorf("app: configure: %w", err)
	}

	application := framework.NewApplication(applicationConfig)
	manifest := framework.NewProviderManifest()
	if err := manifest.Add("bridra.application", providers.NewApplicationServiceProvider()); err != nil {
		return nil, fmt.Errorf("app: provider manifest: %w", err)
	}
	for index, provider := range additionalProviders {
		name := fmt.Sprintf("bridra.extension.%d", index+1)
		if err := manifest.Add(name, provider); err != nil {
			return nil, fmt.Errorf("app: provider manifest: %w", err)
		}
	}
	if err := application.RegisterManifest(manifest); err != nil {
		return nil, abortBuild(application, fmt.Errorf("app: register providers: %w", err))
	}
	if err := application.Boot(); err != nil {
		return nil, abortBuild(application, fmt.Errorf("app: boot providers: %w", err))
	}
	return application, nil
}

func abortBuild(application *framework.Application, buildError error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownError := application.Shutdown(ctx)
	if shutdownError == nil {
		return buildError
	}
	return errors.Join(
		buildError,
		fmt.Errorf("app: shutdown failed application: %w", shutdownError),
	)
}
