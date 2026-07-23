package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/cluion/bridra/backend/app"
	"github.com/cluion/bridra/backend/framework"
)

type sidecarShutdownProvider struct {
	terminated bool
	err        error
}

func (provider *sidecarShutdownProvider) Register(*framework.Application) error {
	return nil
}

func (provider *sidecarShutdownProvider) Terminate(
	context.Context,
	*framework.Application,
) error {
	provider.terminated = true
	return provider.err
}

func TestRunSidecarShutsDownApplicationWhenInputCloses(t *testing.T) {
	provider := &sidecarShutdownProvider{}
	application, err := app.Build(app.Config{Token: "secret"}, provider)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if err := runSidecar(
		context.Background(),
		application,
		strings.NewReader(""),
		io.Discard,
		io.Discard,
	); err != nil {
		t.Fatalf("run sidecar: %v", err)
	}
	if !provider.terminated || !application.ShutdownComplete() {
		t.Fatal("sidecar should terminate the application")
	}
}

func TestRunSidecarReturnsApplicationShutdownFailure(t *testing.T) {
	providerError := errors.New("close resource")
	provider := &sidecarShutdownProvider{err: providerError}
	application, err := app.Build(app.Config{Token: "secret"}, provider)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	err = runSidecar(
		context.Background(),
		application,
		strings.NewReader(""),
		io.Discard,
		io.Discard,
	)
	if !errors.Is(err, providerError) {
		t.Fatalf("run sidecar error = %v, want %v", err, providerError)
	}
}

type blockingSidecarInput struct {
	started   chan struct{}
	closed    chan struct{}
	stopped   chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

func (input *blockingSidecarInput) Read([]byte) (int, error) {
	input.startOnce.Do(func() { close(input.started) })
	<-input.closed
	close(input.stopped)
	return 0, io.EOF
}

func (input *blockingSidecarInput) Close() error {
	input.closeOnce.Do(func() { close(input.closed) })
	return nil
}

type orderedSidecarShutdownProvider struct {
	inputStopped <-chan struct{}
}

func (provider *orderedSidecarShutdownProvider) Register(*framework.Application) error {
	return nil
}

func (provider *orderedSidecarShutdownProvider) Terminate(
	context.Context,
	*framework.Application,
) error {
	select {
	case <-provider.inputStopped:
		return nil
	default:
		return errors.New("application terminated before stdio server stopped")
	}
}

func TestRunSidecarStopsStdioBeforeApplicationOnSignal(t *testing.T) {
	input := &blockingSidecarInput{
		started: make(chan struct{}),
		closed:  make(chan struct{}),
		stopped: make(chan struct{}),
	}
	provider := &orderedSidecarShutdownProvider{inputStopped: input.stopped}
	application, err := app.Build(app.Config{Token: "secret"}, provider)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- runSidecar(ctx, application, input, io.Discard, io.Discard)
	}()

	<-input.started
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("run sidecar: %v", err)
	}
}
