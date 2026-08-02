package framework

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRecoveryReportsPanicWithoutChangingTheStableResponse(t *testing.T) {
	panicValue := errors.New("private panic value")
	var reported CrashReport
	router := NewRouter()
	router.Use(
		Traced("recovery", RecoveryWithReporter(CrashReporterFunc(
			func(_ context.Context, report CrashReport) {
				reported = report
			},
		))),
	)
	router.Handle("reports.build", func(*Context) (any, error) {
		panic(panicValue)
	})

	response := router.Dispatch(
		context.Background(),
		Request{ID: "request-1", Method: "reports.build"},
	)

	if response.Error == nil ||
		response.Error.Code != "internal_error" ||
		strings.Contains(response.Error.Message, panicValue.Error()) {
		t.Fatalf("response = %#v", response)
	}
	if reported.Recovered != panicValue ||
		reported.RequestID != "request-1" ||
		reported.RPCMethod != "reports.build" ||
		reported.OccurredAt.IsZero() ||
		len(reported.Stack) == 0 {
		t.Fatalf("report = %#v", reported)
	}
	if len(reported.Pipeline) != 1 || reported.Pipeline[0] != "recovery:before" {
		t.Fatalf("pipeline = %#v", reported.Pipeline)
	}
}

func TestRecoveryContainsReporterPanics(t *testing.T) {
	router := NewRouter()
	router.Use(RecoveryWithReporter(CrashReporterFunc(
		func(context.Context, CrashReport) {
			panic("reporter failed")
		},
	)))
	router.Handle("panic", func(*Context) (any, error) {
		panic("handler failed")
	})

	response := router.Dispatch(
		context.Background(),
		Request{ID: "1", Method: "panic"},
	)

	if response.Error == nil || response.Error.Code != "internal_error" {
		t.Fatalf("response = %#v", response)
	}
}

func TestRecoveryAcceptsTypedNilReporter(t *testing.T) {
	var reporter *testCrashReporter
	router := NewRouter()
	router.Use(RecoveryWithReporter(reporter))
	router.Handle("panic", func(*Context) (any, error) { panic("boom") })

	response := router.Dispatch(
		context.Background(),
		Request{ID: "1", Method: "panic"},
	)

	if response.Error == nil || response.Error.Code != "internal_error" {
		t.Fatalf("response = %#v", response)
	}
}

type testCrashReporter struct{}

func (*testCrashReporter) ReportCrash(context.Context, CrashReport) {}
