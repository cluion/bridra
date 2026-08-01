package framework_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

func TestPublicHTTPObservabilityAPI(t *testing.T) {
	metrics := framework.NewHTTPMetrics()
	var observed framework.HTTPRequestObservation
	observer := framework.NewHTTPObserverGroup(
		metrics,
		framework.HTTPObserverFuncs{
			EndFunc: func(_ context.Context, result framework.HTTPRequestObservation) {
				observed = result
			},
		},
	)
	var logs bytes.Buffer
	jsonObserver, err := framework.NewJSONHTTPObserver(&logs)
	if err != nil {
		t.Fatalf("new JSON observer: %v", err)
	}
	observer = framework.NewHTTPObserverGroup(observer, jsonObserver)
	handler := &framework.HTTPObservationHandler{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if _, ok := framework.HTTPRequestIDFromContext(request.Context()); !ok {
				t.Fatal("request id is unavailable")
			}
			writer.WriteHeader(http.StatusNoContent)
		}),
		Observer: observer,
	}
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))

	if recorder.Code != http.StatusNoContent || observed.Outcome != framework.HTTPOutcomeSuccess {
		t.Fatalf("status = %d, observation = %#v", recorder.Code, observed)
	}
	if snapshot := metrics.Snapshot(); snapshot.Total != 1 || snapshot.Success != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if logs.Len() == 0 {
		t.Fatal("JSON observer wrote no event")
	}
}
