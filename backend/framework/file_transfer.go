package framework

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	defaultFileTransferTTL      = 15 * time.Minute
	defaultFileTransferMaxBytes = int64(2 << 30)
	fileTransferIDBytes         = 32
)

var (
	ErrFileTransferClosed      = errors.New("framework: file transfer store is closed")
	ErrFileTransferExpired     = errors.New("framework: file transfer has expired")
	ErrFileTransferInvalid     = errors.New("framework: file transfer is invalid")
	ErrFileTransferNotFound    = errors.New("framework: file transfer was not found")
	ErrFileTransferTooLarge    = errors.New("framework: file transfer is too large")
	ErrInvalidFileTransferOpts = errors.New("framework: file transfer options are invalid")
)

var FileTransferStoreKey = NewServiceKey[*FileTransferStore]("framework.file-transfers")

type FileReference struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	MediaType string    `json:"mediaType"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	ExpiresAt time.Time `json:"expiresAt"`
	LocalPath string    `json:"localPath,omitempty"`
}

type FileTransferOptions struct {
	RootDir         string
	TTL             time.Duration
	MaxBytes        int64
	ExposeLocalPath bool
}

func DefaultFileTransferOptions() FileTransferOptions {
	return FileTransferOptions{
		TTL:      defaultFileTransferTTL,
		MaxBytes: defaultFileTransferMaxBytes,
	}
}

type fileTransferEntry struct {
	reference FileReference
	path      string
}

type FileTransferStore struct {
	root            string
	ownsRoot        bool
	ttl             time.Duration
	maxBytes        int64
	exposeLocalPath bool
	now             func() time.Time

	mu      sync.Mutex
	active  sync.WaitGroup
	entries map[string]fileTransferEntry
	closed  bool
}

func NewFileTransferStore(options FileTransferOptions) (*FileTransferStore, error) {
	if options.TTL == 0 {
		options.TTL = defaultFileTransferTTL
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = defaultFileTransferMaxBytes
	}
	if options.TTL < 0 || options.MaxBytes < 0 {
		return nil, ErrInvalidFileTransferOpts
	}

	root := options.RootDir
	ownsRoot := root == ""
	var err error
	if ownsRoot {
		root, err = os.MkdirTemp("", "bridra-files-*")
	} else {
		root, err = filepath.Abs(root)
		if err == nil {
			err = os.MkdirAll(root, 0o700)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("framework: create file transfer directory: %w", err)
	}
	return &FileTransferStore{
		root:            root,
		ownsRoot:        ownsRoot,
		ttl:             options.TTL,
		maxBytes:        options.MaxBytes,
		exposeLocalPath: options.ExposeLocalPath,
		now:             time.Now,
		entries:         make(map[string]fileTransferEntry),
	}, nil
}

func (store *FileTransferStore) Stage(
	ctx context.Context,
	name string,
	mediaType string,
	source io.Reader,
) (FileReference, error) {
	if ctx == nil || source == nil {
		return FileReference{}, ErrFileTransferInvalid
	}
	name, mediaType, err := validateFileTransferMetadata(name, mediaType)
	if err != nil {
		return FileReference{}, err
	}
	if err := store.begin(); err != nil {
		return FileReference{}, err
	}
	defer store.active.Done()

	store.pruneExpired()
	id, path, file, err := store.createFile()
	if err != nil {
		return FileReference{}, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()

	hash := sha256.New()
	written, err := io.Copy(
		io.MultiWriter(file, hash),
		io.LimitReader(contextReader{ctx: ctx, reader: source}, store.maxBytes+1),
	)
	if err != nil {
		return FileReference{}, fmt.Errorf("framework: stage file transfer: %w", err)
	}
	if written > store.maxBytes {
		return FileReference{}, ErrFileTransferTooLarge
	}
	if err := file.Sync(); err != nil {
		return FileReference{}, fmt.Errorf("framework: sync file transfer: %w", err)
	}
	if err := file.Close(); err != nil {
		return FileReference{}, fmt.Errorf("framework: close file transfer: %w", err)
	}

	reference := FileReference{
		ID:        id,
		Name:      name,
		MediaType: mediaType,
		Size:      written,
		SHA256:    hex.EncodeToString(hash.Sum(nil)),
		ExpiresAt: store.now().UTC().Add(store.ttl),
	}
	if store.exposeLocalPath {
		reference.LocalPath = path
	}

	store.mu.Lock()
	if store.closed {
		store.mu.Unlock()
		return FileReference{}, ErrFileTransferClosed
	}
	store.entries[id] = fileTransferEntry{reference: reference, path: path}
	store.mu.Unlock()
	keep = true
	return reference, nil
}

func (store *FileTransferStore) Take(id string) (*FileDownload, error) {
	if !validFileTransferID(id) {
		return nil, ErrFileTransferNotFound
	}
	store.mu.Lock()
	if store.closed {
		store.mu.Unlock()
		return nil, ErrFileTransferClosed
	}
	entry, exists := store.entries[id]
	if exists {
		delete(store.entries, id)
	}
	store.mu.Unlock()
	if !exists {
		return nil, ErrFileTransferNotFound
	}
	if !store.now().Before(entry.reference.ExpiresAt) {
		_ = os.Remove(entry.path)
		return nil, ErrFileTransferExpired
	}
	file, err := os.Open(entry.path)
	if err != nil {
		_ = os.Remove(entry.path)
		return nil, fmt.Errorf("framework: open file transfer: %w", err)
	}
	reference := entry.reference
	reference.LocalPath = ""
	return &FileDownload{
		Reference: reference,
		reader:    file,
		path:      entry.path,
	}, nil
}

func (store *FileTransferStore) Close() error {
	store.mu.Lock()
	if store.closed {
		store.mu.Unlock()
		return nil
	}
	store.closed = true
	store.mu.Unlock()
	store.active.Wait()

	store.mu.Lock()
	paths := make([]string, 0, len(store.entries))
	for id, entry := range store.entries {
		paths = append(paths, entry.path)
		delete(store.entries, id)
	}
	store.mu.Unlock()

	var cleanupErrors []error
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	if store.ownsRoot {
		if err := os.RemoveAll(store.root); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	return errors.Join(cleanupErrors...)
}

func (store *FileTransferStore) begin() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return ErrFileTransferClosed
	}
	store.active.Add(1)
	return nil
}

func (store *FileTransferStore) createFile() (string, string, *os.File, error) {
	for range 4 {
		bytes := make([]byte, fileTransferIDBytes)
		if _, err := rand.Read(bytes); err != nil {
			return "", "", nil, fmt.Errorf("framework: create file transfer id: %w", err)
		}
		id := hex.EncodeToString(bytes)
		path := filepath.Join(store.root, id)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return id, path, file, nil
		}
		if !os.IsExist(err) {
			return "", "", nil, fmt.Errorf("framework: create file transfer: %w", err)
		}
	}
	return "", "", nil, errors.New("framework: could not allocate a unique file transfer id")
}

func (store *FileTransferStore) pruneExpired() {
	now := store.now()
	store.mu.Lock()
	paths := make([]string, 0)
	for id, entry := range store.entries {
		if !now.Before(entry.reference.ExpiresAt) {
			paths = append(paths, entry.path)
			delete(store.entries, id)
		}
	}
	store.mu.Unlock()
	for _, path := range paths {
		_ = os.Remove(path)
	}
}

type FileDownload struct {
	Reference FileReference
	reader    *os.File
	path      string
	once      sync.Once
	closeErr  error
}

func (download *FileDownload) Read(buffer []byte) (int, error) {
	return download.reader.Read(buffer)
}

func (download *FileDownload) Close() error {
	download.once.Do(func() {
		closeErr := download.reader.Close()
		removeErr := os.Remove(download.path)
		if os.IsNotExist(removeErr) {
			removeErr = nil
		}
		download.closeErr = errors.Join(closeErr, removeErr)
	})
	return download.closeErr
}

type FileTransferServiceProvider struct {
	options FileTransferOptions
	store   *FileTransferStore
}

func NewFileTransferServiceProvider(options FileTransferOptions) *FileTransferServiceProvider {
	return &FileTransferServiceProvider{options: options}
}

func (provider *FileTransferServiceProvider) ProviderName() string {
	return "framework.file-transfers"
}

func (provider *FileTransferServiceProvider) Register(application *Application) error {
	store, err := NewFileTransferStore(provider.options)
	if err != nil {
		return err
	}
	if err := Instance(application.Container(), FileTransferStoreKey, store); err != nil {
		_ = store.Close()
		return err
	}
	provider.store = store
	return nil
}

func (provider *FileTransferServiceProvider) Terminate(
	context.Context,
	*Application,
) error {
	if provider.store == nil {
		return nil
	}
	return provider.store.Close()
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	read, err := reader.reader.Read(buffer)
	if err == nil {
		err = reader.ctx.Err()
	}
	return read, err
}

func validateFileTransferMetadata(name, mediaType string) (string, string, error) {
	if name == "" ||
		name == "." ||
		name == ".." ||
		filepath.Base(name) != name ||
		strings.ContainsAny(name, `/\`) ||
		strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", "", ErrFileTransferInvalid
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	parsed, parameters, err := mime.ParseMediaType(mediaType)
	if err != nil {
		return "", "", ErrFileTransferInvalid
	}
	return name, mime.FormatMediaType(parsed, parameters), nil
}

func validFileTransferID(id string) bool {
	if len(id) != fileTransferIDBytes*2 {
		return false
	}
	for _, character := range id {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
