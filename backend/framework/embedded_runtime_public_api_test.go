package framework_test

import (
	"context"
	"testing"
	"time"

	"github.com/cluion/bridra/backend/framework"
)

func TestPublicEmbeddedRuntimeAPI(t *testing.T) {
	started := make(chan struct{})
	router := framework.NewRouter()
	router.Handle("wait", func(ctx *framework.Context) (any, error) {
		close(started)
		<-ctx.Done()
		return "cancelled", nil
	})
	shutdown := framework.EmbeddedRuntimeShutdown(
		func(context.Context) error { return nil },
	)
	runtime, err := framework.NewEmbeddedRuntimeWithOptions(
		router,
		shutdown,
		framework.EmbeddedRuntimeOptions{ShutdownTimeout: time.Second},
	)
	if err != nil {
		t.Fatalf("NewEmbeddedRuntimeWithOptions() error = %v", err)
	}
	callDone := make(chan error, 1)
	go func() {
		_, err := runtime.CallJSON(`{"id":"public-1","method":"wait"}`)
		callDone <- err
	}()
	<-started
	if !runtime.Cancel("public-1") {
		t.Fatal("Cancel() did not find the active public request")
	}
	if err := <-callDone; err != nil {
		t.Fatalf("CallJSON() error = %v", err)
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
