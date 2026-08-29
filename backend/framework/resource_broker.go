package framework

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
)

const (
	defaultResourceBookmarkMaxBytes = 1 << 20
	defaultResourceGrantLimit       = 64
	resourceCapabilityNonceBytes    = 32
	resourceCapabilityMACBytes      = 16
	resourceCapabilityKeyBytes      = 32
	resourceCapabilityBytes         = resourceCapabilityNonceBytes + resourceCapabilityMACBytes
)

var (
	ErrResourceBookmarkUnavailable = errors.New("framework: resource bookmarks are unavailable")
	ErrResourceBookmarkInvalid     = errors.New("framework: resource bookmark is invalid")
	ErrResourceBookmarkTooLarge    = errors.New("framework: resource bookmark is too large")
	ErrResourceBookmarkStale       = errors.New("framework: resource bookmark is stale")
	ErrResourceBookmarkResolve     = errors.New("framework: resource bookmark could not be resolved")
	ErrResourceBookmarkAccess      = errors.New("framework: resource bookmark access was denied")
	ErrResourceBrokerOptions       = errors.New("framework: resource broker options are invalid")
	ErrResourceBrokerClosed        = errors.New("framework: resource broker is closed")
	ErrResourceGrantLimit          = errors.New("framework: resource grant limit was reached")
	ErrResourceCapabilityInvalid   = errors.New("framework: resource capability is invalid")
	ErrResourceCapabilityNotFound  = errors.New("framework: resource capability was not found")
	ErrResourceCapabilityCreate    = errors.New("framework: resource capability could not be created")
	ErrResourceLeaseInvalid        = errors.New("framework: resource lease is invalid")
	ErrResourceLeaseRelease        = errors.New("framework: resource lease could not be released")
)

var ResourceBrokerKey = NewServiceKey[*ResourceBroker]("framework.resource-broker")

// ResourceBookmarkScope selects the lifecycle used to resolve bookmark data.
type ResourceBookmarkScope string

const (
	ResourceBookmarkEphemeral  ResourceBookmarkScope = "ephemeral"
	ResourceBookmarkPersistent ResourceBookmarkScope = "persistent"
)

// ResourceCapability is an opaque, process-local authority for one active lease.
// Applications must not log or persist capability values.
type ResourceCapability string

// ResourceLease retains native authority for a resolved local resource.
type ResourceLease interface {
	LocalPath() string
	Release() error
}

// ResourceBookmarkResolver converts authority-bearing bookmark bytes into a lease.
// Implementations must not include bookmark bytes or resolved paths in errors.
type ResourceBookmarkResolver interface {
	ResolveBookmark([]byte, ResourceBookmarkScope) (ResourceLease, error)
}

type ResourceBrokerOptions struct {
	MaxBookmarkBytes int
	MaxActiveGrants  int
}

func DefaultResourceBrokerOptions() ResourceBrokerOptions {
	return ResourceBrokerOptions{
		MaxBookmarkBytes: defaultResourceBookmarkMaxBytes,
		MaxActiveGrants:  defaultResourceGrantLimit,
	}
}

type resourceLeaseEntry struct {
	lease ResourceLease
	path  string
}

// ResourceBroker owns bounded bookmark leases and exposes only opaque capabilities.
type ResourceBroker struct {
	resolver         ResourceBookmarkResolver
	maxBookmarkBytes int
	maxActiveGrants  int
	capabilityKey    [resourceCapabilityKeyBytes]byte

	mu        sync.Mutex
	active    sync.WaitGroup
	leases    map[ResourceCapability]resourceLeaseEntry
	pending   int
	closed    bool
	closeDone chan struct{}
	closeErr  error
}

func NewResourceBroker(
	resolver ResourceBookmarkResolver,
	options ResourceBrokerOptions,
) (*ResourceBroker, error) {
	if resourceBookmarkResolverIsNil(resolver) {
		return nil, ErrResourceBrokerOptions
	}
	if options.MaxBookmarkBytes == 0 {
		options.MaxBookmarkBytes = defaultResourceBookmarkMaxBytes
	}
	if options.MaxActiveGrants == 0 {
		options.MaxActiveGrants = defaultResourceGrantLimit
	}
	if options.MaxBookmarkBytes < 0 || options.MaxActiveGrants < 0 {
		return nil, ErrResourceBrokerOptions
	}
	broker := &ResourceBroker{
		resolver:         resolver,
		maxBookmarkBytes: options.MaxBookmarkBytes,
		maxActiveGrants:  options.MaxActiveGrants,
		leases:           make(map[ResourceCapability]resourceLeaseEntry),
	}
	if _, err := rand.Read(broker.capabilityKey[:]); err != nil {
		return nil, ErrResourceCapabilityCreate
	}
	return broker, nil
}

// Grant resolves bookmark data and retains its authority until release or shutdown.
func (broker *ResourceBroker) Grant(
	bookmark []byte,
	scope ResourceBookmarkScope,
) (ResourceCapability, error) {
	if len(bookmark) == 0 || !validResourceBookmarkScope(scope) {
		return "", ErrResourceBookmarkInvalid
	}
	if len(bookmark) > broker.maxBookmarkBytes {
		return "", ErrResourceBookmarkTooLarge
	}
	if err := broker.beginGrant(); err != nil {
		return "", err
	}
	defer broker.finishGrant()

	lease, err := broker.resolver.ResolveBookmark(bookmark, scope)
	if err != nil {
		return "", sanitizeResourceBookmarkError(err)
	}
	if resourceLeaseIsNil(lease) {
		return "", ErrResourceLeaseInvalid
	}
	path := lease.LocalPath()
	if strings.TrimSpace(path) == "" {
		return "", errors.Join(ErrResourceLeaseInvalid, releaseResourceLease(lease))
	}

	capability, err := broker.createCapability()
	if err != nil {
		return "", errors.Join(err, releaseResourceLease(lease))
	}

	broker.mu.Lock()
	if broker.closed {
		broker.mu.Unlock()
		return "", errors.Join(ErrResourceBrokerClosed, releaseResourceLease(lease))
	}
	broker.leases[capability] = resourceLeaseEntry{lease: lease, path: path}
	broker.mu.Unlock()
	return capability, nil
}

// ResolvePath returns the local path while the capability's lease is active.
func (broker *ResourceBroker) ResolvePath(capability ResourceCapability) (string, error) {
	if !validResourceCapability(capability) {
		return "", ErrResourceCapabilityInvalid
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if broker.closed {
		return "", ErrResourceBrokerClosed
	}
	entry, ok := broker.leases[capability]
	if !ok {
		return "", ErrResourceCapabilityNotFound
	}
	return entry.path, nil
}

// Release relinquishes one active lease. A recent duplicate release is idempotent.
func (broker *ResourceBroker) Release(capability ResourceCapability) error {
	if !validResourceCapability(capability) {
		return ErrResourceCapabilityInvalid
	}

	broker.mu.Lock()
	if broker.closed {
		broker.mu.Unlock()
		return ErrResourceBrokerClosed
	}
	entry, ok := broker.leases[capability]
	if !ok {
		issued := broker.capabilityBelongsToBroker(capability)
		broker.mu.Unlock()
		if issued {
			return nil
		}
		return ErrResourceCapabilityNotFound
	}
	delete(broker.leases, capability)
	broker.active.Add(1)
	broker.mu.Unlock()

	defer broker.active.Done()
	return releaseResourceLease(entry.lease)
}

// Close rejects new operations and releases every remaining lease exactly once.
func (broker *ResourceBroker) Close() error {
	broker.mu.Lock()
	if broker.closed {
		done := broker.closeDone
		broker.mu.Unlock()
		if done != nil {
			<-done
		}
		broker.mu.Lock()
		err := broker.closeErr
		broker.mu.Unlock()
		return err
	}
	broker.closed = true
	broker.closeDone = make(chan struct{})
	done := broker.closeDone
	broker.mu.Unlock()

	broker.active.Wait()
	broker.mu.Lock()
	leases := make([]ResourceLease, 0, len(broker.leases))
	for capability, entry := range broker.leases {
		leases = append(leases, entry.lease)
		delete(broker.leases, capability)
	}
	broker.mu.Unlock()

	releaseErrors := make([]error, 0)
	for _, lease := range leases {
		if err := releaseResourceLease(lease); err != nil {
			releaseErrors = append(releaseErrors, err)
		}
	}
	closeErr := errors.Join(releaseErrors...)
	broker.mu.Lock()
	broker.closeErr = closeErr
	close(done)
	broker.mu.Unlock()
	return closeErr
}

func (broker *ResourceBroker) beginGrant() error {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if broker.closed {
		return ErrResourceBrokerClosed
	}
	if len(broker.leases)+broker.pending >= broker.maxActiveGrants {
		return ErrResourceGrantLimit
	}
	broker.pending++
	broker.active.Add(1)
	return nil
}

func (broker *ResourceBroker) finishGrant() {
	broker.mu.Lock()
	broker.pending--
	broker.mu.Unlock()
	broker.active.Done()
}

func (broker *ResourceBroker) createCapability() (ResourceCapability, error) {
	for range 4 {
		value := make([]byte, resourceCapabilityBytes)
		nonce := value[:resourceCapabilityNonceBytes]
		if _, err := rand.Read(nonce); err != nil {
			return "", ErrResourceCapabilityCreate
		}
		mac := hmac.New(sha256.New, broker.capabilityKey[:])
		_, _ = mac.Write(nonce)
		copy(value[resourceCapabilityNonceBytes:], mac.Sum(nil)[:resourceCapabilityMACBytes])
		capability := ResourceCapability(hex.EncodeToString(value))
		broker.mu.Lock()
		_, active := broker.leases[capability]
		broker.mu.Unlock()
		if !active {
			return capability, nil
		}
	}
	return "", ErrResourceCapabilityCreate
}

func validResourceBookmarkScope(scope ResourceBookmarkScope) bool {
	return scope == ResourceBookmarkEphemeral || scope == ResourceBookmarkPersistent
}

func validResourceCapability(capability ResourceCapability) bool {
	value := string(capability)
	if len(value) != resourceCapabilityBytes*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (broker *ResourceBroker) capabilityBelongsToBroker(capability ResourceCapability) bool {
	value, err := hex.DecodeString(string(capability))
	if err != nil || len(value) != resourceCapabilityBytes {
		return false
	}
	nonce := value[:resourceCapabilityNonceBytes]
	provided := value[resourceCapabilityNonceBytes:]
	mac := hmac.New(sha256.New, broker.capabilityKey[:])
	_, _ = mac.Write(nonce)
	want := mac.Sum(nil)[:resourceCapabilityMACBytes]
	return hmac.Equal(provided, want)
}

func releaseResourceLease(lease ResourceLease) error {
	if err := lease.Release(); err != nil {
		return ErrResourceLeaseRelease
	}
	return nil
}

func sanitizeResourceBookmarkError(err error) error {
	stable := []error{
		ErrResourceBookmarkUnavailable,
		ErrResourceBookmarkInvalid,
		ErrResourceBookmarkTooLarge,
		ErrResourceBookmarkStale,
		ErrResourceBookmarkResolve,
		ErrResourceBookmarkAccess,
	}
	for _, candidate := range stable {
		if errors.Is(err, candidate) {
			return candidate
		}
	}
	return ErrResourceBookmarkResolve
}

func resourceBookmarkResolverIsNil(resolver ResourceBookmarkResolver) bool {
	return interfaceIsNil(resolver)
}

func resourceLeaseIsNil(lease ResourceLease) bool {
	return interfaceIsNil(lease)
}

type ResourceBrokerServiceProvider struct {
	resolver ResourceBookmarkResolver
	options  ResourceBrokerOptions
	broker   *ResourceBroker
}

func NewResourceBrokerServiceProvider(
	resolver ResourceBookmarkResolver,
	options ResourceBrokerOptions,
) *ResourceBrokerServiceProvider {
	return &ResourceBrokerServiceProvider{resolver: resolver, options: options}
}

func (provider *ResourceBrokerServiceProvider) ProviderName() string {
	return "framework.resource-broker"
}

func (provider *ResourceBrokerServiceProvider) Register(application *Application) error {
	broker, err := NewResourceBroker(provider.resolver, provider.options)
	if err != nil {
		return err
	}
	if err := Instance(application.Container(), ResourceBrokerKey, broker); err != nil {
		return err
	}
	provider.broker = broker
	return nil
}

func (provider *ResourceBrokerServiceProvider) Terminate(
	_ context.Context,
	_ *Application,
) error {
	if provider.broker == nil {
		return nil
	}
	return provider.broker.Close()
}
