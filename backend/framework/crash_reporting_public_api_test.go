package framework_test

import (
	"context"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

func TestPublicCrashReporterAPI(t *testing.T) {
	var report framework.CrashReport
	reporter := framework.CrashReporterFunc(
		func(_ context.Context, received framework.CrashReport) {
			report = received
		},
	)
	router := framework.NewRouter()
	router.Use(framework.RecoveryWithReporter(reporter))
	router.Handle("public.panic", func(*framework.Context) (any, error) {
		panic("boom")
	})

	response := router.Dispatch(context.Background(), framework.Request{
		ID: "public-1", Method: "public.panic",
	})

	if response.Error == nil || response.Error.Code != "internal_error" {
		t.Fatalf("response = %#v", response)
	}
	if report.RequestID != "public-1" ||
		report.RPCMethod != "public.panic" ||
		report.Recovered != "boom" ||
		len(report.Stack) == 0 {
		t.Fatalf("report = %#v", report)
	}
}
