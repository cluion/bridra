// Package mobilebridge is the reference Go binding surface used by the iOS
// Embedded Core proof of concept. Applications should own an equivalent narrow
// package so gomobile exports only lifecycle and JSON RPC entry points while
// the complete Go implementation remains private.
package mobilebridge

import (
	"context"
	"errors"
	"time"

	"github.com/cluion/bridra/backend/app"
	"github.com/cluion/bridra/backend/framework"
)

const embeddedRuntimeName = "Go embedded mobile"

var ErrInvalidCloseTimeout = errors.New("mobilebridge: close timeout must be positive")

type Runtime struct {
	embedded *framework.EmbeddedRuntime
}

func NewRuntime(token string) (*Runtime, error) {
	application, err := app.Build(app.Config{
		Token:   token,
		Runtime: embeddedRuntimeName,
	})
	if err != nil {
		return nil, err
	}
	embedded, err := framework.NewEmbeddedRuntime(
		application.Router(),
		application.Shutdown,
	)
	if err != nil {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return nil, errors.Join(err, application.Shutdown(shutdownContext))
	}
	return &Runtime{embedded: embedded}, nil
}

func FrameworkVersion() string {
	return framework.FrameworkVersion
}

func (runtime *Runtime) CallJSON(requestJSON string) (string, error) {
	if runtime == nil || runtime.embedded == nil {
		return "", framework.ErrEmbeddedRuntimeInvalid
	}
	return runtime.embedded.CallJSON(requestJSON)
}

func (runtime *Runtime) Close(timeoutMilliseconds int64) error {
	if runtime == nil || runtime.embedded == nil {
		return framework.ErrEmbeddedRuntimeInvalid
	}
	if timeoutMilliseconds <= 0 {
		return ErrInvalidCloseTimeout
	}
	ctx, cancel := context.WithTimeout(
		context.Background(),
		time.Duration(timeoutMilliseconds)*time.Millisecond,
	)
	defer cancel()
	return runtime.embedded.Close(ctx)
}
