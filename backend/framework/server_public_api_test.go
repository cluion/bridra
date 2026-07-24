package framework_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

func TestPublicServerConcurrencyOptions(t *testing.T) {
	router := framework.NewRouter()
	router.Handle("public.echo", func(*framework.Context) (any, error) {
		return "ok", nil
	})
	var output bytes.Buffer
	server := &framework.Server{
		Router:                router,
		Input:                 strings.NewReader("{\"id\":\"1\",\"method\":\"public.echo\"}\n"),
		Output:                &output,
		MaxConcurrentRequests: 2,
		MaxPendingRequests:    4,
	}

	if err := server.Serve(context.Background()); err != nil {
		t.Fatalf("serve: %v", err)
	}
	var response framework.Response
	if err := json.NewDecoder(&output).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != "1" || response.Result != "ok" {
		t.Fatalf("response = %#v", response)
	}
}
