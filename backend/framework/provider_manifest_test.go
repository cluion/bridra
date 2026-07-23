package framework

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

type manifestProvider struct {
	name   string
	events *[]string
}

func (provider *manifestProvider) Register(*Application) error {
	*provider.events = append(*provider.events, provider.name+":register")
	return nil
}

func (provider *manifestProvider) Boot(*Application) error {
	*provider.events = append(*provider.events, provider.name+":boot")
	return nil
}

func TestProviderManifestPreservesExplicitLifecycleOrder(t *testing.T) {
	events := []string{}
	manifest := NewProviderManifest()
	if err := manifest.Add("first", &manifestProvider{name: "first", events: &events}); err != nil {
		t.Fatalf("add first: %v", err)
	}
	if err := manifest.Add("second", &manifestProvider{name: "second", events: &events}); err != nil {
		t.Fatalf("add second: %v", err)
	}
	application := NewApplication(nil)

	if err := application.RegisterManifest(manifest); err != nil {
		t.Fatalf("register manifest: %v", err)
	}
	if err := application.Boot(); err != nil {
		t.Fatalf("boot: %v", err)
	}
	want := []string{"first:register", "second:register", "first:boot", "second:boot"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestProviderManifestRejectsDuplicateNamesWithoutMutation(t *testing.T) {
	events := []string{}
	manifest := NewProviderManifest()
	first := &manifestProvider{name: "first", events: &events}
	if err := manifest.Add("app", first); err != nil {
		t.Fatalf("add first: %v", err)
	}

	err := manifest.Add("app", &manifestProvider{name: "duplicate", events: &events})
	if !errors.Is(err, ErrProviderAlreadyDefined) {
		t.Fatalf("error = %v, want ErrProviderAlreadyDefined", err)
	}
	entries := manifest.Entries()
	if len(entries) != 1 || entries[0].Provider != first {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestProviderManifestRejectsTypedNilProvider(t *testing.T) {
	manifest := NewProviderManifest()
	var provider *manifestProvider

	if err := manifest.Add("nil", provider); !errors.Is(err, ErrInvalidProviderManifest) {
		t.Fatalf("error = %v, want ErrInvalidProviderManifest", err)
	}
}

type failingManifestProvider struct {
	err error
}

func (provider failingManifestProvider) Register(*Application) error {
	return provider.err
}

func TestProviderManifestNamesRegistrationFailures(t *testing.T) {
	want := errors.New("database unavailable")
	manifest := NewProviderManifest()
	if err := manifest.Add("database", failingManifestProvider{err: want}); err != nil {
		t.Fatalf("add provider: %v", err)
	}

	err := NewApplication(nil).RegisterManifest(manifest)
	if !errors.Is(err, want) || !strings.Contains(err.Error(), `provider "database"`) {
		t.Fatalf("error = %v", err)
	}
}
