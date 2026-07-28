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
	emptyFileSHA256             = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

var (
	ErrFileTransferClosed      = errors.New("framework: file transfer store is closed")
	ErrFileTransferExpired     = errors.New("framework: file transfer has expired")
	ErrFileTransferBusy        = errors.New("framework: file transfer is busy")
	ErrFileTransferChecksum    = errors.New("framework: file transfer checksum does not match")
	ErrFileTransferIncomplete  = errors.New("framework: file transfer is incomplete")
	ErrFileTransferInvalid     = errors.New("framework: file transfer is invalid")
	ErrFileTransferNotFound    = errors.New("framework: file transfer was not found")
	ErrFileTransferOffset      = errors.New("framework: file transfer offset does not match")
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
	offset    int64
	upload    bool
	complete  bool
	busy      bool
}

type FileUploadStatus struct {
	Reference FileReference `json:"file"`
	Offset    int64         `json:"offset"`
	Complete  bool          `json:"complete"`
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
	store.entries[id] = fileTransferEntry{
		reference: reference,
		path:      path,
		offset:    written,
		complete:  true,
	}
	store.mu.Unlock()
	keep = true
	return reference, nil
}

func (store *FileTransferStore) Take(id string) (*FileDownload, error) {
	if !validFileTransferID(id) {
		return nil, ErrFileTransferNotFound
	}
	if err := store.begin(); err != nil {
		return nil, err
	}
	store.mu.Lock()
	if store.closed {
		store.mu.Unlock()
		store.active.Done()
		return nil, ErrFileTransferClosed
	}
	entry, exists := store.entries[id]
	if exists && !entry.complete {
		store.mu.Unlock()
		store.active.Done()
		return nil, ErrFileTransferIncomplete
	}
	if exists && entry.busy {
		store.mu.Unlock()
		store.active.Done()
		return nil, ErrFileTransferBusy
	}
	if exists {
		delete(store.entries, id)
	}
	store.mu.Unlock()
	if !exists {
		store.active.Done()
		return nil, ErrFileTransferNotFound
	}
	if !store.now().Before(entry.reference.ExpiresAt) {
		_ = os.Remove(entry.path)
		store.active.Done()
		return nil, ErrFileTransferExpired
	}
	file, err := os.Open(entry.path)
	if err != nil {
		_ = os.Remove(entry.path)
		store.active.Done()
		return nil, fmt.Errorf("framework: open file transfer: %w", err)
	}
	reference := entry.reference
	reference.LocalPath = ""
	return &FileDownload{
		Reference: reference,
		reader:    file,
		path:      entry.path,
		active:    &store.active,
		consume:   true,
	}, nil
}

func (store *FileTransferStore) BeginUpload(
	name string,
	mediaType string,
	size int64,
	checksum string,
) (FileUploadStatus, error) {
	name, mediaType, err := validateFileTransferMetadata(name, mediaType)
	if err != nil || size < 0 || !validFileTransferChecksum(checksum) {
		return FileUploadStatus{}, ErrFileTransferInvalid
	}
	if size > store.maxBytes {
		return FileUploadStatus{}, ErrFileTransferTooLarge
	}
	if err := store.begin(); err != nil {
		return FileUploadStatus{}, err
	}
	defer store.active.Done()

	store.pruneExpired()
	id, path, file, err := store.createFile()
	if err != nil {
		return FileUploadStatus{}, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return FileUploadStatus{}, fmt.Errorf("framework: close file upload: %w", err)
	}

	reference := FileReference{
		ID:        id,
		Name:      name,
		MediaType: mediaType,
		Size:      size,
		SHA256:    checksum,
		ExpiresAt: store.now().UTC().Add(store.ttl),
	}
	entry := fileTransferEntry{
		reference: reference,
		path:      path,
		upload:    true,
		complete:  size == 0,
	}
	if entry.complete && checksum != emptyFileSHA256 {
		_ = os.Remove(path)
		return FileUploadStatus{}, ErrFileTransferChecksum
	}

	store.mu.Lock()
	if store.closed {
		store.mu.Unlock()
		_ = os.Remove(path)
		return FileUploadStatus{}, ErrFileTransferClosed
	}
	store.entries[id] = entry
	store.mu.Unlock()
	return uploadStatus(entry), nil
}

func (store *FileTransferStore) UploadStatus(id string) (FileUploadStatus, error) {
	if !validFileTransferID(id) {
		return FileUploadStatus{}, ErrFileTransferNotFound
	}
	store.mu.Lock()
	if store.closed {
		store.mu.Unlock()
		return FileUploadStatus{}, ErrFileTransferClosed
	}
	entry, exists := store.entries[id]
	if exists && !entry.upload {
		store.mu.Unlock()
		return FileUploadStatus{}, ErrFileTransferNotFound
	}
	expired := exists && !store.now().Before(entry.reference.ExpiresAt)
	if expired {
		delete(store.entries, id)
	}
	store.mu.Unlock()
	if expired {
		_ = os.Remove(entry.path)
		return FileUploadStatus{}, ErrFileTransferExpired
	}
	if !exists {
		return FileUploadStatus{}, ErrFileTransferNotFound
	}
	return uploadStatus(entry), nil
}

func (store *FileTransferStore) AppendUpload(
	ctx context.Context,
	id string,
	offset int64,
	source io.Reader,
) (FileUploadStatus, error) {
	if ctx == nil || source == nil || offset < 0 || !validFileTransferID(id) {
		return FileUploadStatus{}, ErrFileTransferInvalid
	}
	if err := store.begin(); err != nil {
		return FileUploadStatus{}, err
	}
	defer store.active.Done()

	store.mu.Lock()
	entry, exists := store.entries[id]
	switch {
	case store.closed:
		store.mu.Unlock()
		return FileUploadStatus{}, ErrFileTransferClosed
	case !exists || !entry.upload:
		store.mu.Unlock()
		return FileUploadStatus{}, ErrFileTransferNotFound
	case !store.now().Before(entry.reference.ExpiresAt):
		delete(store.entries, id)
		store.mu.Unlock()
		_ = os.Remove(entry.path)
		return FileUploadStatus{}, ErrFileTransferExpired
	case entry.busy:
		store.mu.Unlock()
		return uploadStatus(entry), ErrFileTransferBusy
	case offset != entry.offset:
		store.mu.Unlock()
		return uploadStatus(entry), ErrFileTransferOffset
	case entry.complete:
		store.mu.Unlock()
		return uploadStatus(entry), nil
	}
	entry.busy = true
	store.entries[id] = entry
	store.mu.Unlock()

	file, err := os.OpenFile(entry.path, os.O_WRONLY, 0o600)
	if err != nil {
		store.releaseUpload(id, entry.path, entry.offset, false)
		return FileUploadStatus{}, fmt.Errorf("framework: open file upload: %w", err)
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		_ = file.Close()
		store.releaseUpload(id, entry.path, entry.offset, false)
		return FileUploadStatus{}, fmt.Errorf("framework: seek file upload: %w", err)
	}
	remaining := entry.reference.Size - offset
	written, copyErr := io.Copy(
		file,
		io.LimitReader(contextReader{ctx: ctx, reader: source}, remaining+1),
	)
	syncErr := file.Sync()
	closeErr := file.Close()
	newOffset := offset + written
	if written > remaining {
		store.releaseUpload(id, entry.path, newOffset, true)
		return FileUploadStatus{}, ErrFileTransferTooLarge
	}
	if syncErr != nil || closeErr != nil {
		store.releaseUpload(id, entry.path, newOffset, true)
		return FileUploadStatus{}, fmt.Errorf(
			"framework: persist file upload: %w",
			errors.Join(syncErr, closeErr),
		)
	}

	complete := newOffset == entry.reference.Size
	if complete {
		checksum, err := fileSHA256(entry.path)
		if err != nil {
			store.releaseUpload(id, entry.path, newOffset, true)
			return FileUploadStatus{}, err
		}
		if checksum != entry.reference.SHA256 {
			store.releaseUpload(id, entry.path, newOffset, true)
			return FileUploadStatus{}, ErrFileTransferChecksum
		}
	}
	status := store.releaseUpload(id, entry.path, newOffset, false)
	if copyErr != nil {
		return status, fmt.Errorf("framework: append file upload: %w", copyErr)
	}
	return status, nil
}

func (store *FileTransferStore) ConsumeUpload(
	reference FileReference,
) (*FileDownload, error) {
	if !validFileTransferID(reference.ID) {
		return nil, ErrFileTransferNotFound
	}
	store.mu.Lock()
	entry, exists := store.entries[reference.ID]
	valid := exists &&
		entry.upload &&
		entry.complete &&
		entry.reference.Name == reference.Name &&
		entry.reference.MediaType == reference.MediaType &&
		entry.reference.Size == reference.Size &&
		entry.reference.SHA256 == reference.SHA256
	store.mu.Unlock()
	if !valid {
		if exists && !entry.complete {
			return nil, ErrFileTransferIncomplete
		}
		return nil, ErrFileTransferInvalid
	}
	return store.Take(reference.ID)
}

func (store *FileTransferStore) ImportUpload(
	ctx context.Context,
	name string,
	mediaType string,
	size int64,
	checksum string,
	source io.Reader,
) (FileReference, error) {
	if ctx == nil || source == nil {
		return FileReference{}, ErrFileTransferInvalid
	}
	status, err := store.BeginUpload(name, mediaType, size, checksum)
	if err != nil {
		return FileReference{}, err
	}
	if size == 0 {
		var probe [1]byte
		read, readErr := (contextReader{ctx: ctx, reader: source}).Read(probe[:])
		if read > 0 {
			store.discard(status.Reference.ID)
			return FileReference{}, ErrFileTransferTooLarge
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			store.discard(status.Reference.ID)
			return FileReference{}, fmt.Errorf("framework: import file upload: %w", readErr)
		}
		return status.Reference, nil
	}
	status, err = store.AppendUpload(ctx, status.Reference.ID, 0, source)
	if err != nil {
		store.discard(status.Reference.ID)
		return FileReference{}, err
	}
	if !status.Complete {
		store.discard(status.Reference.ID)
		return FileReference{}, ErrFileTransferIncomplete
	}
	return status.Reference, nil
}

func (store *FileTransferStore) OpenDownload(
	id string,
	offset int64,
) (*FileDownload, error) {
	if !validFileTransferID(id) || offset < 0 {
		return nil, ErrFileTransferNotFound
	}
	if err := store.begin(); err != nil {
		return nil, err
	}
	store.mu.Lock()
	entry, exists := store.entries[id]
	switch {
	case store.closed:
		store.mu.Unlock()
		store.active.Done()
		return nil, ErrFileTransferClosed
	case !exists || entry.upload || !entry.complete:
		store.mu.Unlock()
		store.active.Done()
		return nil, ErrFileTransferNotFound
	case !store.now().Before(entry.reference.ExpiresAt):
		delete(store.entries, id)
		store.mu.Unlock()
		_ = os.Remove(entry.path)
		store.active.Done()
		return nil, ErrFileTransferExpired
	case entry.busy:
		store.mu.Unlock()
		store.active.Done()
		return nil, ErrFileTransferBusy
	case offset > entry.reference.Size:
		store.mu.Unlock()
		store.active.Done()
		return nil, ErrFileTransferOffset
	}
	entry.busy = true
	store.entries[id] = entry
	store.mu.Unlock()

	file, err := os.Open(entry.path)
	if err != nil {
		store.finishDownload(id, entry.path, false)
		store.active.Done()
		return nil, fmt.Errorf("framework: open file transfer: %w", err)
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		_ = file.Close()
		store.finishDownload(id, entry.path, false)
		store.active.Done()
		return nil, fmt.Errorf("framework: seek file transfer: %w", err)
	}
	reference := entry.reference
	reference.LocalPath = ""
	return &FileDownload{
		Reference: reference,
		reader:    file,
		path:      entry.path,
		store:     store,
		id:        id,
		active:    &store.active,
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
		if !entry.busy && !now.Before(entry.reference.ExpiresAt) {
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
	store     *FileTransferStore
	id        string
	active    *sync.WaitGroup
	consume   bool
	once      sync.Once
	closeErr  error
}

func (download *FileDownload) Read(buffer []byte) (int, error) {
	return download.reader.Read(buffer)
}

func (download *FileDownload) Close() error {
	download.once.Do(func() {
		closeErr := download.reader.Close()
		var cleanupErr error
		switch {
		case download.store != nil:
			cleanupErr = download.store.finishDownload(
				download.id,
				download.path,
				download.consume,
			)
		case download.consume:
			cleanupErr = os.Remove(download.path)
			if os.IsNotExist(cleanupErr) {
				cleanupErr = nil
			}
		}
		download.closeErr = errors.Join(closeErr, cleanupErr)
		if download.active != nil {
			download.active.Done()
		}
	})
	return download.closeErr
}

func (download *FileDownload) Commit() error {
	download.consume = true
	return download.Close()
}

func (store *FileTransferStore) finishDownload(
	id string,
	filePath string,
	consume bool,
) error {
	store.mu.Lock()
	entry, exists := store.entries[id]
	if exists && entry.path == filePath {
		if consume {
			delete(store.entries, id)
		} else {
			entry.busy = false
			store.entries[id] = entry
		}
	}
	store.mu.Unlock()
	if !consume {
		return nil
	}
	err := os.Remove(filePath)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (store *FileTransferStore) releaseUpload(
	id string,
	filePath string,
	offset int64,
	remove bool,
) FileUploadStatus {
	store.mu.Lock()
	entry, exists := store.entries[id]
	if exists && entry.path == filePath {
		if remove {
			delete(store.entries, id)
		} else {
			entry.offset = offset
			entry.complete = offset == entry.reference.Size
			entry.busy = false
			entry.reference.ExpiresAt = store.now().UTC().Add(store.ttl)
			store.entries[id] = entry
		}
	}
	store.mu.Unlock()
	if remove {
		_ = os.Remove(filePath)
		return FileUploadStatus{}
	}
	return uploadStatus(entry)
}

func (store *FileTransferStore) discard(id string) {
	store.mu.Lock()
	entry, exists := store.entries[id]
	if exists {
		delete(store.entries, id)
	}
	store.mu.Unlock()
	if exists {
		_ = os.Remove(entry.path)
	}
}

func uploadStatus(entry fileTransferEntry) FileUploadStatus {
	reference := entry.reference
	reference.LocalPath = ""
	return FileUploadStatus{
		Reference: reference,
		Offset:    entry.offset,
		Complete:  entry.complete,
	}
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("framework: open file upload for verification: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("framework: verify file upload: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
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

func validFileTransferChecksum(checksum string) bool {
	return validFileTransferID(checksum)
}
