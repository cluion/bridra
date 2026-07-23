package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cluion/bridra/backend/framework"
)

var (
	errDevInvalid       = errors.New("dev: invalid configuration")
	errDevBackendExited = errors.New("dev: backend exited unexpectedly")
)

type devTransport string

const (
	devTransportAuto    devTransport = "auto"
	devTransportSidecar devTransport = "sidecar"
	devTransportHTTP    devTransport = "http"
)

type devProcessSpec struct {
	Name        string
	Arguments   []string
	Directory   string
	Environment []string
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
}

type devProcess interface {
	Wait() error
	Signal(os.Signal) error
	Kill() error
}

type devSystem struct {
	goos         string
	stdin        io.Reader
	readyTimeout time.Duration
	stopTimeout  time.Duration
	abs          func(string) (string, error)
	stat         func(string) (os.FileInfo, error)
	mkdirAll     func(string, os.FileMode) error
	run          func(devProcessSpec) error
	start        func(devProcessSpec) (devProcess, error)
	waitReady    func(string, time.Duration) error
	signals      func() (<-chan os.Signal, func())
}

type devCommand struct {
	system devSystem
}

type devOptions struct {
	root         string
	device       string
	transport    devTransport
	listen       string
	readyAddress string
	backendURL   string
	token        string
	corsOrigin   string
	sidecarPath  string
	serverPath   string
}

type execDevProcess struct {
	command *exec.Cmd
}

type runningDevProcess struct {
	name    string
	process devProcess
	done    bool
}

type devProcessExit struct {
	process *runningDevProcess
	err     error
}

func defaultDevSystem() devSystem {
	return devSystem{
		goos:         runtime.GOOS,
		stdin:        os.Stdin,
		readyTimeout: 10 * time.Second,
		stopTimeout:  5 * time.Second,
		abs:          filepath.Abs,
		stat:         os.Stat,
		mkdirAll:     os.MkdirAll,
		run: func(specification devProcessSpec) error {
			return newDevExecCommand(specification).Run()
		},
		start: func(specification devProcessSpec) (devProcess, error) {
			command := newDevExecCommand(specification)
			if err := command.Start(); err != nil {
				return nil, err
			}
			return &execDevProcess{command: command}, nil
		},
		waitReady: waitForTCP,
		signals: func() (<-chan os.Signal, func()) {
			notifications := make(chan os.Signal, 1)
			signal.Notify(notifications, os.Interrupt, syscall.SIGTERM)
			return notifications, func() {
				signal.Stop(notifications)
			}
		},
	}
}

func newDevExecCommand(specification devProcessSpec) *exec.Cmd {
	command := exec.Command(specification.Name, specification.Arguments...)
	configureDevCommand(command)
	command.Dir = specification.Directory
	command.Env = append(os.Environ(), specification.Environment...)
	command.Stdin = specification.Stdin
	command.Stdout = specification.Stdout
	command.Stderr = specification.Stderr
	return command
}

func (process *execDevProcess) Wait() error {
	return process.command.Wait()
}

func (process *execDevProcess) Signal(value os.Signal) error {
	return signalDevProcess(process.command.Process, value)
}

func (process *execDevProcess) Kill() error {
	return killDevProcess(process.command.Process)
}

func (devCommand) name() string {
	return "dev"
}

func (devCommand) summary() string {
	return "Run Flutter and the Go backend as one development session"
}

func (devCommand) usage() string {
	return `Usage:
  bridra dev [options]

Options:
  --root path           Bridra project root (default .)
  --device id           Flutter device id (default current host desktop)
  --transport mode      auto, sidecar, or http (default auto)
  --listen address      Local HTTP backend address (default 127.0.0.1:8080)
  --backend-url URL     URL compiled into Flutter for HTTP mode
  --token value         Development backend token (default dev-token)
  --cors-origin origin  Browser origin allowed by HTTP mode (default *)

Auto mode uses Sidecar for linux, macos, and windows devices. Other devices use
the local HTTP backend. Supplying --backend-url also selects HTTP in auto mode.
Mobile and custom devices require an explicit URL reachable from that device.`
}

func (item devCommand) run(arguments []string, stdout, stderr io.Writer) error {
	if helpRequested(arguments) {
		fmt.Fprintln(stdout, item.usage())
		return nil
	}
	flags := flag.NewFlagSet(item.name(), flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, item.usage())
	}
	root := flags.String("root", ".", "Bridra project root")
	device := flags.String("device", "", "Flutter device id")
	transport := flags.String("transport", string(devTransportAuto), "auto, sidecar, or http")
	listen := flags.String("listen", "127.0.0.1:8080", "local HTTP backend address")
	backendURL := flags.String("backend-url", "", "URL compiled into Flutter for HTTP mode")
	token := flags.String("token", "dev-token", "development backend token")
	corsOrigin := flags.String("cors-origin", "*", "allowed browser origin")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("%w: dev: %v", errUsage, err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%w: dev: unexpected arguments: %v", errUsage, flags.Args())
	}

	options, err := item.resolveOptions(devOptions{
		root:       *root,
		device:     *device,
		transport:  devTransport(*transport),
		listen:     *listen,
		backendURL: *backendURL,
		token:      *token,
		corsOrigin: *corsOrigin,
	})
	if err != nil {
		return err
	}
	return item.develop(options, stdout, stderr)
}

func (item devCommand) resolveOptions(options devOptions) (devOptions, error) {
	absoluteRoot, err := item.system.abs(options.root)
	if err != nil {
		return devOptions{}, fmt.Errorf("dev: resolve project root: %w", err)
	}
	options.root = filepath.Clean(absoluteRoot)
	if _, err := loadProjectMetadata(options.root); err != nil {
		return devOptions{}, fmt.Errorf("%w: project metadata: %w", errDevInvalid, err)
	}
	for _, required := range []string{".fvmrc", "pubspec.yaml"} {
		information, err := item.system.stat(filepath.Join(options.root, required))
		if err != nil {
			return devOptions{}, fmt.Errorf("%w: %s is unavailable: %v", errDevInvalid, required, err)
		}
		if information.IsDir() {
			return devOptions{}, fmt.Errorf("%w: %s must be a file", errDevInvalid, required)
		}
	}

	options.device = strings.TrimSpace(options.device)
	if options.device == "" {
		options.device, err = hostDesktopDevice(item.system.goos)
		if err != nil {
			return devOptions{}, err
		}
	}
	options.transport = devTransport(strings.ToLower(strings.TrimSpace(string(options.transport))))
	switch options.transport {
	case devTransportAuto:
		if strings.TrimSpace(options.backendURL) != "" {
			options.transport = devTransportHTTP
		} else if isDesktopDevice(options.device) {
			options.transport = devTransportSidecar
		} else {
			options.transport = devTransportHTTP
		}
	case devTransportSidecar, devTransportHTTP:
	default:
		return devOptions{}, fmt.Errorf(
			"%w: transport must be auto, sidecar, or http",
			errDevInvalid,
		)
	}

	suffix := ""
	if item.system.goos == "windows" {
		suffix = ".exe"
	}
	options.sidecarPath = filepath.Join(options.root, "build", "sidecar", "bridra_backend"+suffix)
	options.serverPath = filepath.Join(options.root, "build", "server", "bridra_server"+suffix)

	switch options.transport {
	case devTransportSidecar:
		expected, err := hostDesktopDevice(item.system.goos)
		if err != nil {
			return devOptions{}, err
		}
		if options.device != expected {
			return devOptions{}, fmt.Errorf(
				"%w: sidecar device %q is unavailable on %s; expected %q",
				errDevInvalid,
				options.device,
				item.system.goos,
				expected,
			)
		}
		if strings.TrimSpace(options.backendURL) != "" {
			return devOptions{}, fmt.Errorf(
				"%w: --backend-url requires HTTP transport",
				errDevInvalid,
			)
		}
	case devTransportHTTP:
		options.listen = strings.TrimSpace(options.listen)
		options.backendURL = strings.TrimSpace(options.backendURL)
		options.corsOrigin = strings.TrimSpace(options.corsOrigin)
		readyAddress, err := localReadyAddress(options.listen)
		if err != nil {
			return devOptions{}, err
		}
		options.readyAddress = readyAddress
		if options.backendURL == "" {
			if !isDesktopDevice(options.device) && !isWebDevice(options.device) {
				return devOptions{}, fmt.Errorf(
					"%w: device %q requires an explicit --backend-url reachable from the device",
					errDevInvalid,
					options.device,
				)
			}
			options.backendURL = "http://" + readyAddress + "/rpc"
		}
		if err := validateDevelopmentBackendURL(options.backendURL); err != nil {
			return devOptions{}, err
		}
		if err := validateDevelopmentBackendBinding(options.listen, options.backendURL); err != nil {
			return devOptions{}, err
		}
		if strings.TrimSpace(options.token) == "" {
			return devOptions{}, fmt.Errorf("%w: token cannot be empty", errDevInvalid)
		}
		if options.corsOrigin == "" {
			return devOptions{}, fmt.Errorf("%w: CORS origin cannot be empty", errDevInvalid)
		}
	}
	return options, nil
}

func hostDesktopDevice(goos string) (string, error) {
	switch goos {
	case "linux":
		return "linux", nil
	case "darwin":
		return "macos", nil
	case "windows":
		return "windows", nil
	default:
		return "", fmt.Errorf(
			"%w: no default desktop device for host %s; pass --device and --transport http",
			errDevInvalid,
			goos,
		)
	}
}

func isDesktopDevice(device string) bool {
	switch device {
	case "linux", "macos", "windows":
		return true
	default:
		return false
	}
}

func isWebDevice(device string) bool {
	switch device {
	case "chrome", "edge", "web-server":
		return true
	default:
		return false
	}
}

func localReadyAddress(listen string) (string, error) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		return "", fmt.Errorf("%w: invalid listen address: %v", errDevInvalid, err)
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil || number == 0 {
		return "", fmt.Errorf("%w: listen port must be between 1 and 65535", errDevInvalid)
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port), nil
}

func validateDevelopmentBackendURL(value string) error {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
		return fmt.Errorf(
			"%w: backend URL must be an absolute http:// URL",
			errDevInvalid,
		)
	}
	if parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" || parsed.Path != "/rpc" {
		return fmt.Errorf(
			"%w: backend URL must target /rpc without credentials, query parameters, or fragments",
			errDevInvalid,
		)
	}
	return nil
}

func validateDevelopmentBackendBinding(listen string, backendURL string) error {
	listenHost, listenPort, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		return fmt.Errorf("%w: invalid listen address: %v", errDevInvalid, err)
	}
	parsed, err := url.ParseRequestURI(strings.TrimSpace(backendURL))
	if err != nil {
		return fmt.Errorf("%w: invalid backend URL: %v", errDevInvalid, err)
	}
	backendPort := parsed.Port()
	if backendPort == "" {
		backendPort = "80"
	}
	if backendPort != listenPort {
		return fmt.Errorf(
			"%w: backend URL port %s does not match listen port %s",
			errDevInvalid,
			backendPort,
			listenPort,
		)
	}
	if isLoopbackHost(listenHost) && !isLoopbackHost(parsed.Hostname()) {
		return fmt.Errorf(
			"%w: listen address %s is not reachable through backend URL host %s",
			errDevInvalid,
			listenHost,
			parsed.Hostname(),
		)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func (item devCommand) develop(options devOptions, stdout, stderr io.Writer) error {
	fmt.Fprintf(stdout, "Bridra Dev %s\n", framework.FrameworkVersion)
	fmt.Fprintf(stdout, "Project: %s\n", options.root)
	fmt.Fprintf(stdout, "Device: %s\n", options.device)
	fmt.Fprintf(stdout, "Transport: %s\n\n", options.transport)

	switch options.transport {
	case devTransportSidecar:
		if err := item.buildBackend(
			options,
			"Sidecar",
			options.sidecarPath,
			"./cmd/sidecar",
			stdout,
			stderr,
		); err != nil {
			return err
		}
		return item.runDesktop(options, stdout, stderr)
	case devTransportHTTP:
		if err := item.buildBackend(
			options,
			"HTTP backend",
			options.serverPath,
			"./cmd/server",
			stdout,
			stderr,
		); err != nil {
			return err
		}
		return item.runHTTP(options, stdout, stderr)
	default:
		return fmt.Errorf("%w: unresolved transport", errDevInvalid)
	}
}

func (item devCommand) buildBackend(
	options devOptions,
	label string,
	output string,
	entrypoint string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	if err := item.system.mkdirAll(filepath.Dir(output), 0o755); err != nil {
		return fmt.Errorf("dev: create %s output directory: %w", label, err)
	}
	fmt.Fprintf(stdout, "Building Go %s...\n", label)
	err := item.system.run(devProcessSpec{
		Name:        "go",
		Arguments:   []string{"build", "-trimpath", "-o", output, entrypoint},
		Directory:   filepath.Join(options.root, "backend"),
		Environment: []string{"CGO_ENABLED=0"},
		Stdout:      stdout,
		Stderr:      stderr,
	})
	if err != nil {
		return fmt.Errorf("dev: build %s: %w", label, err)
	}
	return nil
}

func (item devCommand) runDesktop(
	options devOptions,
	stdout io.Writer,
	stderr io.Writer,
) error {
	fmt.Fprintf(stdout, "Starting Flutter on %s...\n", options.device)
	flutter, err := item.system.start(devProcessSpec{
		Name:        "fvm",
		Arguments:   []string{"flutter", "run", "-d", options.device},
		Directory:   options.root,
		Environment: []string{"BRIDRA_SIDECAR_PATH=" + options.sidecarPath},
		Stdin:       item.system.stdin,
		Stdout:      stdout,
		Stderr:      stderr,
	})
	if err != nil {
		return fmt.Errorf("dev: start Flutter: %w", err)
	}
	processes, events := watchDevProcesses(
		&runningDevProcess{name: "Flutter", process: flutter},
	)
	return item.supervise(processes, events, stdout)
}

func (item devCommand) runHTTP(
	options devOptions,
	stdout io.Writer,
	stderr io.Writer,
) error {
	fmt.Fprintf(stdout, "Starting Go HTTP backend at %s...\n", options.listen)
	backend, err := item.system.start(devProcessSpec{
		Name:        options.serverPath,
		Arguments:   []string{"--listen", options.listen, "--cors-origin", options.corsOrigin},
		Directory:   options.root,
		Environment: []string{"BRIDRA_BACKEND_TOKEN=" + options.token},
		Stdout:      stdout,
		Stderr:      stderr,
	})
	if err != nil {
		return fmt.Errorf("dev: start HTTP backend: %w", err)
	}
	backendProcess := &runningDevProcess{name: "Go HTTP backend", process: backend}
	processes, events := watchDevProcesses(backendProcess)
	ready := make(chan error, 1)
	go func() {
		ready <- item.system.waitReady(options.readyAddress, item.system.readyTimeout)
	}()
	select {
	case event := <-events:
		event.process.done = true
		return backendExitError(event.err)
	case err := <-ready:
		if err != nil {
			cleanupError := item.stopProcesses(processes, events, os.Interrupt)
			return errors.Join(fmt.Errorf("dev: wait for HTTP backend: %w", err), cleanupError)
		}
	}

	fmt.Fprintf(stdout, "Starting Flutter on %s with %s...\n", options.device, options.backendURL)
	flutter, err := item.system.start(devProcessSpec{
		Name: "fvm",
		Arguments: []string{
			"flutter", "run", "-d", options.device,
			"--dart-define=BRIDRA_BACKEND_URL=" + options.backendURL,
			"--dart-define=BRIDRA_BACKEND_TOKEN=" + options.token,
		},
		Directory: options.root,
		Stdin:     item.system.stdin,
		Stdout:    stdout,
		Stderr:    stderr,
	})
	if err != nil {
		cleanupError := item.stopProcesses(processes, events, os.Interrupt)
		return errors.Join(fmt.Errorf("dev: start Flutter: %w", err), cleanupError)
	}
	flutterProcess := &runningDevProcess{name: "Flutter", process: flutter}
	processes = append(processes, flutterProcess)
	go waitForDevProcess(flutterProcess, events)
	return item.supervise(processes, events, stdout)
}

func watchDevProcesses(
	processes ...*runningDevProcess,
) ([]*runningDevProcess, chan devProcessExit) {
	events := make(chan devProcessExit, len(processes)+1)
	for _, process := range processes {
		go waitForDevProcess(process, events)
	}
	return processes, events
}

func waitForDevProcess(process *runningDevProcess, events chan<- devProcessExit) {
	events <- devProcessExit{process: process, err: process.process.Wait()}
}

func (item devCommand) supervise(
	processes []*runningDevProcess,
	events <-chan devProcessExit,
	stdout io.Writer,
) error {
	signals, stopSignals := item.system.signals()
	defer stopSignals()
	for {
		select {
		case received := <-signals:
			fmt.Fprintln(stdout, "\nStopping Bridra development session...")
			return item.stopProcesses(processes, events, received)
		case event := <-events:
			event.process.done = true
			switch event.process.name {
			case "Flutter":
				cleanupError := item.stopProcesses(processes, events, os.Interrupt)
				if event.err != nil {
					return errors.Join(fmt.Errorf("dev: Flutter: %w", event.err), cleanupError)
				}
				return cleanupError
			default:
				cleanupError := item.stopProcesses(processes, events, os.Interrupt)
				return errors.Join(backendExitError(event.err), cleanupError)
			}
		}
	}
}

func backendExitError(err error) error {
	if err == nil {
		return errDevBackendExited
	}
	return fmt.Errorf("%w: %w", errDevBackendExited, err)
}

func (item devCommand) stopProcesses(
	processes []*runningDevProcess,
	events <-chan devProcessExit,
	shutdownSignal os.Signal,
) error {
	var operationErrors []error
	remaining := 0
	for _, process := range processes {
		if process.done {
			continue
		}
		remaining++
		if err := process.process.Signal(shutdownSignal); err != nil && !errors.Is(err, os.ErrProcessDone) {
			operationErrors = append(
				operationErrors,
				fmt.Errorf("dev: signal %s: %w", process.name, err),
			)
		}
	}
	if remaining == 0 {
		return errors.Join(operationErrors...)
	}

	timer := time.NewTimer(item.system.stopTimeout)
	defer timer.Stop()
	killed := false
	for remaining > 0 {
		select {
		case event := <-events:
			if !event.process.done {
				event.process.done = true
				remaining--
			}
		case <-timer.C:
			if killed {
				operationErrors = append(
					operationErrors,
					errors.New("dev: child processes did not exit after kill"),
				)
				return errors.Join(operationErrors...)
			}
			for _, process := range processes {
				if process.done {
					continue
				}
				if err := process.process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
					operationErrors = append(
						operationErrors,
						fmt.Errorf("dev: kill %s: %w", process.name, err),
					)
				}
			}
			killed = true
			timer.Reset(item.system.stopTimeout)
		}
	}
	return errors.Join(operationErrors...)
}

func waitForTCP(address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastError error
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
		if err == nil {
			return connection.Close()
		}
		lastError = err
		time.Sleep(50 * time.Millisecond)
	}
	if lastError == nil {
		lastError = errors.New("readiness timeout elapsed")
	}
	return fmt.Errorf("timed out connecting to %s: %w", address, lastError)
}
