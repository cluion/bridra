package framework

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

var (
	ErrInvalidProviderManifest = errors.New("framework: provider manifest is invalid")
	ErrInvalidProviderName     = errors.New("framework: provider name is invalid")
	ErrProviderAlreadyDefined  = errors.New("framework: provider is already defined")
)

type ProviderEntry struct {
	Name     string
	Provider ServiceProvider
}

type ProviderManifest struct {
	mu      sync.RWMutex
	entries []ProviderEntry
	names   map[string]struct{}
}

func NewProviderManifest() *ProviderManifest {
	return &ProviderManifest{names: make(map[string]struct{})}
}

func (manifest *ProviderManifest) Add(name string, provider ServiceProvider) error {
	if manifest == nil || serviceProviderIsNil(provider) {
		return ErrInvalidProviderManifest
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrInvalidProviderName
	}
	manifest.mu.Lock()
	defer manifest.mu.Unlock()
	if manifest.names == nil {
		manifest.names = make(map[string]struct{})
	}
	if _, exists := manifest.names[name]; exists {
		return fmt.Errorf("%w: %s", ErrProviderAlreadyDefined, name)
	}
	manifest.names[name] = struct{}{}
	manifest.entries = append(manifest.entries, ProviderEntry{Name: name, Provider: provider})
	return nil
}

func (manifest *ProviderManifest) Entries() []ProviderEntry {
	if manifest == nil {
		return nil
	}
	manifest.mu.RLock()
	entries := append([]ProviderEntry(nil), manifest.entries...)
	manifest.mu.RUnlock()
	return entries
}

func (application *Application) RegisterManifest(manifest *ProviderManifest) error {
	if manifest == nil {
		return ErrInvalidProviderManifest
	}
	entries := manifest.Entries()
	providers := make([]ServiceProvider, 0, len(entries))
	for _, entry := range entries {
		provider := manifestServiceProvider{entry: entry}
		if _, ok := entry.Provider.(TerminableServiceProvider); ok {
			providers = append(providers, manifestTerminableServiceProvider{
				manifestServiceProvider: provider,
			})
			continue
		}
		providers = append(providers, provider)
	}
	return application.Register(providers...)
}

type manifestServiceProvider struct {
	entry ProviderEntry
}

func (provider manifestServiceProvider) ProviderName() string {
	return provider.entry.Name
}

func (provider manifestServiceProvider) Register(application *Application) error {
	if err := provider.entry.Provider.Register(application); err != nil {
		return fmt.Errorf("provider %q: %w", provider.entry.Name, err)
	}
	return nil
}

func (provider manifestServiceProvider) Boot(application *Application) error {
	bootable, ok := provider.entry.Provider.(BootableServiceProvider)
	if !ok {
		return nil
	}
	if err := bootable.Boot(application); err != nil {
		return fmt.Errorf("provider %q: %w", provider.entry.Name, err)
	}
	return nil
}

type manifestTerminableServiceProvider struct {
	manifestServiceProvider
}

func (provider manifestTerminableServiceProvider) Terminate(
	ctx context.Context,
	application *Application,
) error {
	terminable := provider.entry.Provider.(TerminableServiceProvider)
	return terminable.Terminate(ctx, application)
}
