package framework

import (
	"context"
	"reflect"
	"runtime/debug"
	"time"
)

type CrashReport struct {
	OccurredAt time.Time
	RequestID  string
	RPCMethod  string
	Pipeline   []string
	Recovered  any
	Stack      []byte
}

type CrashReporter interface {
	ReportCrash(context.Context, CrashReport)
}

type CrashReporterFunc func(context.Context, CrashReport)

func (reporter CrashReporterFunc) ReportCrash(
	ctx context.Context,
	report CrashReport,
) {
	reporter(ctx, report)
}

func RecoveryWithReporter(reporter CrashReporter) Middleware {
	return func(next Handler) Handler {
		return func(ctx *Context) (result any, err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					reportRecoveredCrash(ctx, reporter, recovered)
					result = nil
					err = NewError(
						"internal_error",
						"The Go backend recovered from a panic.",
					)
				}
			}()
			return next(ctx)
		}
	}
}

func reportRecoveredCrash(
	ctx *Context,
	reporter CrashReporter,
	recovered any,
) {
	if crashReporterIsNil(reporter) {
		return
	}
	report := CrashReport{
		OccurredAt: time.Now().UTC(),
		Recovered:  recovered,
		Stack:      append([]byte(nil), debug.Stack()...),
	}
	parent := context.Background()
	if ctx != nil {
		if ctx.Context != nil {
			parent = ctx.Context
		}
		report.RequestID = ctx.Request.ID
		report.RPCMethod = ctx.Request.Method
		report.Pipeline = append([]string(nil), ctx.Trace...)
	}

	defer func() {
		_ = recover()
	}()
	reporter.ReportCrash(parent, report)
}

func crashReporterIsNil(reporter CrashReporter) bool {
	if reporter == nil {
		return true
	}
	value := reflect.ValueOf(reporter)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
