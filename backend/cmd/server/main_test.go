package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

func TestBuildApplicationLoadsTokenFromEnvironment(t *testing.T) {
	t.Setenv("BRIDRA_BACKEND_TOKEN", "environment-token")

	application, err := buildApplication("", false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Cleanup(func() {
		if err := shutdownApplication(application); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	response := application.Router().Dispatch(context.Background(), framework.Request{
		ID: "1", Method: "system.health", Meta: map[string]string{"token": "environment-token"},
	})
	if response.Error != nil {
		t.Fatalf("response error: %v", response.Error)
	}
}

func TestBuildApplicationExplicitTokenOverridesEnvironment(t *testing.T) {
	t.Setenv("BRIDRA_BACKEND_TOKEN", "environment-token")

	application, err := buildApplication("runtime-token", true)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Cleanup(func() {
		if err := shutdownApplication(application); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	response := application.Router().Dispatch(context.Background(), framework.Request{
		ID: "1", Method: "system.health", Meta: map[string]string{"token": "runtime-token"},
	})
	if response.Error != nil {
		t.Fatalf("response error: %v", response.Error)
	}
}

func TestBuildApplicationReportsMissingTokenAsConfigError(t *testing.T) {
	t.Setenv("BRIDRA_BACKEND_TOKEN", "")

	_, err := buildApplication("", false)
	var loadErrors *framework.ConfigLoadErrors
	if !errors.As(err, &loadErrors) {
		t.Fatalf("error = %v, want ConfigLoadErrors", err)
	}
}

func TestSmokeStreamEmitsOrderedProgressAndData(t *testing.T) {
	router := framework.NewRouter()
	registerSmokeStream(router)

	var responses []framework.Response
	err := router.DispatchStream(
		context.Background(),
		framework.Request{ID: "1", Method: smokeStreamMethod},
		func(response framework.Response) error {
			responses = append(responses, response)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("dispatch stream: %v", err)
	}
	if len(responses) != 6 {
		t.Fatalf("responses = %#v", responses)
	}
	if responses[0].Stream == nil || responses[0].Stream.Progress == nil ||
		responses[0].Stream.Progress.Completed != 0 ||
		responses[1].Stream == nil || responses[1].Result == nil ||
		responses[4].Stream == nil || responses[4].Stream.Progress == nil ||
		responses[4].Stream.Progress.Completed != 2 ||
		responses[5].Stream == nil || responses[5].Stream.Kind != "complete" {
		t.Fatalf("responses = %#v", responses)
	}
}

func TestSmokeStreamUsesApplicationAuthentication(t *testing.T) {
	application, err := buildApplication("secret", true)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Cleanup(func() {
		if err := shutdownApplication(application); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	registerSmokeStream(application.Router())

	var responses []framework.Response
	err = application.Router().DispatchStream(
		context.Background(),
		framework.Request{
			ID:     "1",
			Method: smokeStreamMethod,
			Meta:   map[string]string{"token": "wrong"},
		},
		func(response framework.Response) error {
			responses = append(responses, response)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("dispatch stream: %v", err)
	}
	if len(responses) != 1 || responses[0].Error == nil ||
		responses[0].Error.Code != "unauthorized" ||
		responses[0].Stream == nil || responses[0].Stream.Kind != "complete" {
		t.Fatalf("responses = %#v", responses)
	}
}

func TestSmokeDownloadStagesKnownManagedFile(t *testing.T) {
	store, err := framework.NewFileTransferStore(
		framework.DefaultFileTransferOptions(),
	)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	router := framework.NewRouter()
	registerSmokeDownload(router, store)

	response := router.Dispatch(context.Background(), framework.Request{
		ID: "1", Method: smokeDownloadMethod,
	})
	if response.Error != nil {
		t.Fatalf("response error: %v", response.Error)
	}
	reference, ok := response.Result.(framework.FileReference)
	if !ok {
		t.Fatalf("result = %#v", response.Result)
	}
	if reference.Name != smokeDownloadName ||
		reference.MediaType != smokeDownloadMediaType ||
		reference.Size != 61440 ||
		reference.SHA256 != "1d07674b0ad481aad8ca8bfb35963b10d765add489fc10c8e4645958023da950" ||
		reference.LocalPath != "" {
		t.Fatalf("reference = %#v", reference)
	}

	download, err := store.Take(reference.ID)
	if err != nil {
		t.Fatalf("take download: %v", err)
	}
	content, readErr := io.ReadAll(download)
	closeErr := download.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatalf("read download: %v", err)
	}
	want := strings.Repeat(smokeDownloadBlock, smokeDownloadBlockCount)
	if string(content) != want {
		t.Fatalf("downloaded %d unexpected bytes", len(content))
	}
}

func TestSmokeDownloadUsesApplicationAuthentication(t *testing.T) {
	application, err := buildApplication("secret", true)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Cleanup(func() {
		if err := shutdownApplication(application); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	store, err := framework.Resolve(
		application.Container(),
		framework.FileTransferStoreKey,
	)
	if err != nil {
		t.Fatalf("resolve store: %v", err)
	}
	registerSmokeDownload(application.Router(), store)

	response := application.Router().Dispatch(
		context.Background(),
		framework.Request{
			ID:     "1",
			Method: smokeDownloadMethod,
			Meta:   map[string]string{"token": "wrong"},
		},
	)
	if response.Error == nil || response.Error.Code != "unauthorized" {
		t.Fatalf("response = %#v", response)
	}
}
