package framework_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

type publicShutdownProvider struct {
	name         string
	terminations *[]string
	err          error
}

func (provider *publicShutdownProvider) Register(*framework.Application) error {
	return nil
}

func (provider *publicShutdownProvider) Terminate(
	context.Context,
	*framework.Application,
) error {
	*provider.terminations = append(*provider.terminations, provider.name)
	return provider.err
}

var _ framework.TerminableServiceProvider = (*publicShutdownProvider)(nil)

func TestPublicApplicationShutdownAPI(t *testing.T) {
	terminations := []string{}
	providerError := errors.New("close database")
	manifest := framework.NewProviderManifest()
	if err := manifest.Add("public.first", &publicShutdownProvider{
		name:         "first",
		terminations: &terminations,
	}); err != nil {
		t.Fatalf("add first: %v", err)
	}
	if err := manifest.Add("public.database", &publicShutdownProvider{
		name:         "database",
		terminations: &terminations,
		err:          providerError,
	}); err != nil {
		t.Fatalf("add database: %v", err)
	}
	application := framework.NewApplication(nil)
	if err := application.RegisterManifest(manifest); err != nil {
		t.Fatalf("register manifest: %v", err)
	}
	if err := application.Boot(); err != nil {
		t.Fatalf("boot: %v", err)
	}

	err := application.Shutdown(context.Background())
	if !errors.Is(err, framework.ErrApplicationShutdownFailed) || !errors.Is(err, providerError) {
		t.Fatalf("shutdown error = %v", err)
	}
	var failures *framework.ApplicationShutdownErrors
	if !errors.As(err, &failures) {
		t.Fatalf("shutdown error type = %T", err)
	}
	if len(failures.Failures) != 1 || failures.Failures[0].Provider != "public.database" {
		t.Fatalf("failures = %#v", failures.Failures)
	}
	if !reflect.DeepEqual(terminations, []string{"database", "first"}) {
		t.Fatalf("terminations = %#v", terminations)
	}
	if !application.ShutdownComplete() {
		t.Fatal("shutdown should be complete")
	}
}
