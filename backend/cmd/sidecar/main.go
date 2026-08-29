package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cluion/bridra/backend/app"
	"github.com/cluion/bridra/backend/app/settings"
	"github.com/cluion/bridra/backend/framework"
)

func main() {
	launch, err := framework.ReadSidecarLaunch(os.Args[1:], os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sidecar: launch: %v\n", err)
		os.Exit(2)
	}
	if launch.UsesStdinHandshake {
		fmt.Fprintln(os.Stderr, framework.SidecarLaunchReadyMessage)
	}

	application, err := app.Build(app.Config{
		Token:   launch.Token,
		Logs:    os.Stderr,
		Runtime: "Go sidecar",
	}, framework.NewFileTransferServiceProvider(framework.FileTransferOptions{
		ExposeLocalPath: true,
	}), framework.NewResourceBrokerServiceProvider(
		framework.NewMacOSResourceBookmarkResolver(),
		framework.DefaultResourceBrokerOptions(),
	))
	if err != nil {
		fmt.Fprintf(os.Stderr, "sidecar: configure: %v\n", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, stopParent, err := framework.ParentProcessContext(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sidecar: parent lifecycle: %v\n", err)
		os.Exit(2)
	}
	defer stopParent()
	runError := runSidecar(ctx, application, launch.Input, os.Stdout, os.Stderr)
	cause := context.Cause(ctx)
	if cause != nil &&
		!errors.Is(cause, context.Canceled) &&
		!errors.Is(cause, framework.ErrParentProcessExited) {
		runError = errors.Join(runError, cause)
	}
	if runError != nil {
		fmt.Fprintf(os.Stderr, "sidecar: %v\n", runError)
		os.Exit(1)
	}
}

func runSidecar(
	ctx context.Context,
	application *framework.Application,
	input io.Reader,
	output io.Writer,
	logs io.Writer,
) error {
	fileTransfers, _ := framework.Resolve(
		application.Container(),
		framework.FileTransferStoreKey,
	)
	resources, _ := framework.Resolve(
		application.Container(),
		framework.ResourceBrokerKey,
	)
	server := &framework.Server{
		Router:        application.Router(),
		Input:         input,
		Output:        output,
		Errors:        logs,
		FileTransfers: fileTransfers,
		Resources:     resources,
		Token: framework.ConfigValue(
			application.Config(),
			settings.BackendToken,
		),
	}
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(ctx)
	}()

	var serveError error
	select {
	case serveError = <-serveErrors:
	case <-ctx.Done():
		if closer, ok := input.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				serveError = fmt.Errorf("close input: %w", err)
			} else {
				timer := time.NewTimer(5 * time.Second)
				select {
				case <-serveErrors:
				case <-timer.C:
					serveError = errors.New("stdio server did not stop before shutdown")
				}
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			}
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownError := application.Shutdown(shutdownCtx)
	return errors.Join(serveError, shutdownError)
}
