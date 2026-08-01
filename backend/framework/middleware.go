package framework

import (
	"fmt"
	"io"
	"log/slog"
	"time"
)

func Traced(name string, middleware Middleware) Middleware {
	return func(next Handler) Handler {
		wrapped := middleware(next)
		return func(ctx *Context) (result any, err error) {
			ctx.Trace = append(ctx.Trace, name+":before")
			defer func() {
				ctx.Trace = append(ctx.Trace, name+":after")
			}()
			return wrapped(ctx)
		}
	}
}

func Recovery() Middleware {
	return func(next Handler) Handler {
		return func(ctx *Context) (result any, err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					result = nil
					err = NewError("internal_error", "The Go backend recovered from a panic.")
				}
			}()
			return next(ctx)
		}
	}
}

func RequireRequestID() Middleware {
	return func(next Handler) Handler {
		return func(ctx *Context) (any, error) {
			if ctx.Request.ID == "" {
				return nil, NewError("invalid_request", "A request id is required.")
			}
			return next(ctx)
		}
	}
}

func Authenticate(expectedToken string) Middleware {
	return func(next Handler) Handler {
		return func(ctx *Context) (any, error) {
			_, authenticated := PrincipalFromContext(ctx)
			if !authenticated &&
				(expectedToken == "" || ctx.Request.Meta["token"] != expectedToken) {
				return nil, NewError("unauthorized", "The backend token is missing or invalid.")
			}
			return next(ctx)
		}
	}
}

func LogRequests(output io.Writer) Middleware {
	logger := slog.New(slog.NewJSONHandler(output, nil))
	return func(next Handler) Handler {
		return func(ctx *Context) (result any, err error) {
			startedAt := time.Now()
			defer func() {
				logger.Info(
					"rpc request",
					"id", ctx.Request.ID,
					"method", ctx.Request.Method,
					"duration_ms", time.Since(startedAt).Milliseconds(),
					"error", errorText(err),
				)
			}()
			return next(ctx)
		}
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprint(err)
}
