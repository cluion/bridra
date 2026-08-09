package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cluion/bridra/backend/app"
	"github.com/cluion/bridra/backend/app/settings"
	"github.com/cluion/bridra/backend/framework"
)

func main() {
	listenAddress := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	token := flag.String(
		"token",
		"",
		"token required in every RPC request; overrides BRIDRA_BACKEND_TOKEN",
	)
	allowedOrigin := flag.String("cors-origin", "", "allowed browser origin; use * for local development")
	smokeStream := flag.Bool(
		"smoke-stream",
		false,
		"enable the authenticated streaming route used by platform smoke tests",
	)
	smokeDownload := flag.Bool(
		"smoke-download",
		false,
		"enable the authenticated managed-download route used by platform smoke tests",
	)
	smokeUploadResume := flag.Bool(
		"smoke-upload-resume",
		false,
		"enable authenticated upload verification with one injected interruption",
	)
	flag.Parse()
	tokenProvided := false
	flag.Visit(func(value *flag.Flag) {
		if value.Name == "token" {
			tokenProvided = true
		}
	})
	application, err := buildApplication(*token, tokenProvided)
	if err != nil {
		fmt.Fprintf(os.Stderr, "server: configure: %v\n", err)
		os.Exit(2)
	}
	fileTransfers, err := framework.Resolve(
		application.Container(),
		framework.FileTransferStoreKey,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "server: file transfers: %v\n", err)
		if shutdownErr := shutdownApplication(application); shutdownErr != nil {
			fmt.Fprintf(os.Stderr, "server: application shutdown: %v\n", shutdownErr)
		}
		os.Exit(2)
	}
	if *smokeStream {
		registerSmokeStream(application.Router())
	}
	if *smokeDownload {
		registerSmokeDownload(application.Router(), fileTransfers)
	}
	if *smokeUploadResume {
		registerSmokeUploadVerification(application.Router(), fileTransfers)
	}
	backendToken := framework.ConfigValue(
		application.Config(),
		settings.BackendToken,
	)
	httpPrincipal, err := framework.NewPrincipal("bridra-http-client")
	if err != nil {
		fmt.Fprintf(os.Stderr, "server: HTTP principal: %v\n", err)
		if shutdownErr := shutdownApplication(application); shutdownErr != nil {
			fmt.Fprintf(os.Stderr, "server: application shutdown: %v\n", shutdownErr)
		}
		os.Exit(2)
	}
	httpAuthenticator, err := framework.NewStaticTokenAuthenticator(
		backendToken,
		httpPrincipal,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "server: HTTP authenticator: %v\n", err)
		if shutdownErr := shutdownApplication(application); shutdownErr != nil {
			fmt.Fprintf(os.Stderr, "server: application shutdown: %v\n", shutdownErr)
		}
		os.Exit(2)
	}
	httpRateLimiter, err := framework.NewMemoryRateLimiter(
		framework.DefaultMemoryRateLimiterOptions(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "server: HTTP rate limiter: %v\n", err)
		if shutdownErr := shutdownApplication(application); shutdownErr != nil {
			fmt.Fprintf(os.Stderr, "server: application shutdown: %v\n", shutdownErr)
		}
		os.Exit(2)
	}
	httpObserver, err := framework.NewJSONHTTPObserver(os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "server: HTTP observer: %v\n", err)
		if shutdownErr := shutdownApplication(application); shutdownErr != nil {
			fmt.Fprintf(os.Stderr, "server: application shutdown: %v\n", shutdownErr)
		}
		os.Exit(2)
	}

	mux := http.NewServeMux()
	mux.Handle("/rpc", &framework.HTTPHandler{
		Router:        application.Router(),
		Authenticator: httpAuthenticator,
		RateLimiter:   httpRateLimiter,
		AllowedOrigin: *allowedOrigin,
		Errors:        os.Stderr,
	})
	fileTransferHandler := http.Handler(&framework.FileTransferHTTPHandler{
		Store:         fileTransfers,
		AllowedOrigin: *allowedOrigin,
		Token:         backendToken,
		Errors:        os.Stderr,
	})
	if *smokeUploadResume {
		fileTransferHandler = &smokeUploadResumeHandler{
			handler:     fileTransferHandler,
			errorOutput: os.Stderr,
		}
	}
	mux.Handle("/rpc/files/", fileTransferHandler)

	server := &http.Server{
		Addr: *listenAddress,
		Handler: &framework.HTTPObservationHandler{
			Handler:  mux,
			Observer: httpObserver,
			Errors:   os.Stderr,
		},
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Minute,
		WriteTimeout:      15 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          log.New(os.Stderr, "server: ", 0),
	}
	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		fmt.Fprintf(os.Stderr, "server: listen: %v\n", err)
		if shutdownErr := shutdownApplication(application); shutdownErr != nil {
			fmt.Fprintf(os.Stderr, "server: application shutdown: %v\n", shutdownErr)
		}
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(os.Stderr, "server: listening on http://%s/rpc\n", listener.Addr())
		errCh <- server.Serve(listener)
	}()

	var serveError error
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			serveError = err
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			serveError = fmt.Errorf("HTTP shutdown: %w", err)
		}
	}
	applicationShutdownError := shutdownApplication(application)
	if err := errors.Join(serveError, applicationShutdownError); err != nil {
		fmt.Fprintf(os.Stderr, "server: %v\n", err)
		os.Exit(1)
	}
}

func shutdownApplication(application *framework.Application) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return application.Shutdown(shutdownCtx)
}

func buildApplication(token string, tokenProvided bool) (*framework.Application, error) {
	overrides := map[string]any{
		settings.LogOutput.Name():   os.Stderr,
		settings.RuntimeName.Name(): "Go HTTP server",
	}
	if tokenProvided {
		overrides[settings.BackendToken.Name()] = token
	}
	return app.BuildFromSources([]framework.ConfigSource{
		framework.NewEnvironmentConfigSource("BRIDRA_"),
		framework.NewMapConfigSource("runtime", overrides),
	}, framework.NewFileTransferServiceProvider(
		framework.DefaultFileTransferOptions(),
	))
}

const smokeStreamMethod = "bridra.smoke.stream"

const (
	smokeDownloadMethod     = "bridra.smoke.download"
	smokeDownloadName       = "bridra-smoke.bin"
	smokeDownloadMediaType  = "application/octet-stream"
	smokeDownloadBlock      = "bridra-managed-download-smoke|"
	smokeDownloadBlockCount = 2048
	smokeUploadVerifyMethod = "bridra.smoke.upload.verify"
	smokeUploadInterruptAt  = int64(32 << 10)
)

var errSmokeUploadInterrupted = errors.New("smoke upload stream interrupted")

func registerSmokeStream(router *framework.Router) {
	router.Handle(smokeStreamMethod, func(ctx *framework.Context) (any, error) {
		return framework.ProduceStream(ctx, func(stream *framework.StreamWriter) error {
			frames := []struct {
				completed int64
				item      string
			}{
				{completed: 0, item: "first"},
				{completed: 1, item: "second"},
			}
			for _, frame := range frames {
				if err := stream.Report(framework.Progress{
					Completed: frame.completed,
					Total:     int64(len(frames)),
					Message:   "Streaming platform smoke",
					Unit:      "items",
				}); err != nil {
					return err
				}
				if err := stream.Send(map[string]any{"item": frame.item}); err != nil {
					return err
				}
			}
			return stream.Report(framework.Progress{
				Completed: int64(len(frames)),
				Total:     int64(len(frames)),
				Message:   "Streaming platform smoke complete",
				Unit:      "items",
			})
		})
	})
}

func registerSmokeDownload(
	router *framework.Router,
	store *framework.FileTransferStore,
) {
	router.Handle(smokeDownloadMethod, func(ctx *framework.Context) (any, error) {
		return store.Stage(
			ctx,
			smokeDownloadName,
			smokeDownloadMediaType,
			strings.NewReader(strings.Repeat(
				smokeDownloadBlock,
				smokeDownloadBlockCount,
			)),
		)
	})
}

func registerSmokeUploadVerification(
	router *framework.Router,
	store *framework.FileTransferStore,
) {
	router.Handle(smokeUploadVerifyMethod, func(ctx *framework.Context) (any, error) {
		params, err := framework.BindParams[struct {
			File framework.FileReference `json:"file"`
		}](ctx)
		if err != nil {
			return nil, err
		}
		upload, err := store.ConsumeUpload(params.File)
		if err != nil {
			return nil, err
		}
		digest := sha256.New()
		size, readErr := io.Copy(digest, upload)
		closeErr := upload.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return nil, err
		}
		return map[string]any{
			"name":   params.File.Name,
			"size":   size,
			"sha256": hex.EncodeToString(digest.Sum(nil)),
		}, nil
	})
}

type smokeUploadResumeHandler struct {
	handler     http.Handler
	errorOutput io.Writer
	interrupted atomic.Bool
	resumed     atomic.Bool
}

func (handler *smokeUploadResumeHandler) ServeHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.Method == http.MethodPatch &&
		request.Header.Get("Upload-Offset") == "0" &&
		handler.interrupted.CompareAndSwap(false, true) {
		request.Body = &smokeInterruptedReadCloser{
			ReadCloser: request.Body,
			remaining:  smokeUploadInterruptAt,
		}
		handler.handler.ServeHTTP(writer, request)
		fmt.Fprintf(
			handler.errorOutput,
			"server: smoke upload interrupted at offset %d\n",
			smokeUploadInterruptAt,
		)
		return
	}
	if request.Method == http.MethodPatch &&
		request.Header.Get("Upload-Offset") == fmt.Sprint(smokeUploadInterruptAt) &&
		handler.resumed.CompareAndSwap(false, true) {
		fmt.Fprintf(
			handler.errorOutput,
			"server: smoke upload resumed at offset %d\n",
			smokeUploadInterruptAt,
		)
	}
	handler.handler.ServeHTTP(writer, request)
}

type smokeInterruptedReadCloser struct {
	io.ReadCloser
	remaining int64
}

func (reader *smokeInterruptedReadCloser) Read(buffer []byte) (int, error) {
	if reader.remaining == 0 {
		return 0, errSmokeUploadInterrupted
	}
	if int64(len(buffer)) > reader.remaining {
		buffer = buffer[:reader.remaining]
	}
	read, err := reader.ReadCloser.Read(buffer)
	reader.remaining -= int64(read)
	return read, err
}
