package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDevRunsDesktopWithBuiltSidecar(t *testing.T) {
	root := devProjectRoot(t)
	flutter := newFakeDevProcess(false)
	flutter.finish(nil)
	harness := newDevHarness(flutter)
	command := devCommand{system: harness.system()}
	var stdout bytes.Buffer

	if err := command.run([]string{"--root", root}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("dev: %v", err)
	}
	runs, starts := harness.specifications()
	if len(runs) != 1 || len(starts) != 1 {
		t.Fatalf("runs = %#v, starts = %#v", runs, starts)
	}
	if got := specificationCommand(runs[0]); !strings.Contains(got, "go build -trimpath") ||
		!strings.Contains(got, "./cmd/sidecar") {
		t.Fatalf("build command = %q", got)
	}
	if got := specificationCommand(starts[0]); got != "fvm flutter run -d linux" {
		t.Fatalf("Flutter command = %q", got)
	}
	if !containsString(
		starts[0].Environment,
		"BRIDRA_SIDECAR_PATH="+filepath.Join(root, "build", "sidecar", "bridra_backend"),
	) {
		t.Fatalf("Flutter environment = %#v", starts[0].Environment)
	}
	if !strings.Contains(stdout.String(), "Transport: sidecar") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestDevRunsHTTPBackendAndCleansItAfterFlutterExits(t *testing.T) {
	root := devProjectRoot(t)
	backend := newFakeDevProcess(true)
	flutter := newFakeDevProcess(false)
	flutter.finish(nil)
	harness := newDevHarness(backend, flutter)
	command := devCommand{system: harness.system()}

	err := command.run(
		[]string{
			"--root", root,
			"--device", "chrome",
			"--listen", "0.0.0.0:9090",
			"--backend-url", "http://192.0.2.10:9090/rpc",
			"--token", "local-token",
			"--cors-origin", "http://localhost:3000",
		},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err != nil {
		t.Fatalf("dev HTTP: %v", err)
	}
	runs, starts := harness.specifications()
	if len(runs) != 1 || !strings.Contains(specificationCommand(runs[0]), "./cmd/server") {
		t.Fatalf("build runs = %#v", runs)
	}
	if len(starts) != 2 {
		t.Fatalf("starts = %#v", starts)
	}
	if starts[0].Name != filepath.Join(root, "build", "server", "bridra_server") {
		t.Fatalf("backend executable = %q", starts[0].Name)
	}
	if got := strings.Join(starts[0].Arguments, " "); got !=
		"--listen 0.0.0.0:9090 --cors-origin http://localhost:3000" {
		t.Fatalf("backend arguments = %q", got)
	}
	if !containsString(starts[0].Environment, "BRIDRA_BACKEND_TOKEN=local-token") {
		t.Fatalf("backend environment = %#v", starts[0].Environment)
	}
	flutterCommand := specificationCommand(starts[1])
	for _, expected := range []string{
		"fvm flutter run -d chrome",
		"--dart-define=BRIDRA_BACKEND_URL=http://192.0.2.10:9090/rpc",
		"--dart-define=BRIDRA_BACKEND_TOKEN=local-token",
	} {
		if !strings.Contains(flutterCommand, expected) {
			t.Fatalf("Flutter command = %q, want %q", flutterCommand, expected)
		}
	}
	if ready := harness.readyAddresses(); len(ready) != 1 || ready[0] != "127.0.0.1:9090" {
		t.Fatalf("readiness addresses = %#v", ready)
	}
	if signals := backend.receivedSignals(); len(signals) != 1 || signals[0] != os.Interrupt {
		t.Fatalf("backend signals = %#v", signals)
	}
}

func TestDevForwardsInterruptAndWaitsForFlutter(t *testing.T) {
	root := devProjectRoot(t)
	flutter := newFakeDevProcess(true)
	harness := newDevHarness(flutter)
	harness.notifications <- os.Interrupt
	var stdout bytes.Buffer

	err := (devCommand{system: harness.system()}).run(
		[]string{"--root", root},
		&stdout,
		&bytes.Buffer{},
	)
	if err != nil {
		t.Fatalf("dev interrupt: %v", err)
	}
	if signals := flutter.receivedSignals(); len(signals) != 1 || signals[0] != os.Interrupt {
		t.Fatalf("Flutter signals = %#v", signals)
	}
	if !strings.Contains(stdout.String(), "Stopping Bridra development session") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestDevCleansBackendWhenFlutterStartFails(t *testing.T) {
	root := devProjectRoot(t)
	backend := newFakeDevProcess(true)
	harness := newDevHarness(backend)
	startFailure := errors.New("Flutter start failed")
	harness.startErrors[1] = startFailure

	err := (devCommand{system: harness.system()}).run(
		[]string{"--root", root, "--device", "chrome"},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if !errors.Is(err, startFailure) {
		t.Fatalf("error = %v, want Flutter start failure", err)
	}
	if signals := backend.receivedSignals(); len(signals) != 1 || signals[0] != os.Interrupt {
		t.Fatalf("backend signals = %#v", signals)
	}
}

func TestDevReportsBackendExitAndStopsFlutter(t *testing.T) {
	root := devProjectRoot(t)
	backend := newFakeDevProcess(false)
	flutter := newFakeDevProcess(true)
	harness := newDevHarness(backend, flutter)
	backendFailure := errors.New("backend crashed")
	harness.afterStart = func(index int) {
		if index == 1 {
			backend.finish(backendFailure)
		}
	}

	err := (devCommand{system: harness.system()}).run(
		[]string{"--root", root, "--device", "chrome"},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if !errors.Is(err, errDevBackendExited) || !errors.Is(err, backendFailure) {
		t.Fatalf("error = %v, want backend failure", err)
	}
	if signals := flutter.receivedSignals(); len(signals) != 1 || signals[0] != os.Interrupt {
		t.Fatalf("Flutter signals = %#v", signals)
	}
}

func TestDevKillsChildThatIgnoresGracefulSignal(t *testing.T) {
	process := newFakeDevProcess(false)
	running := &runningDevProcess{name: "stuck", process: process}
	processes, events := watchDevProcesses(running)
	command := devCommand{system: devSystem{stopTimeout: time.Millisecond}}

	if err := command.stopProcesses(processes, events, os.Interrupt); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if process.killCount() != 1 {
		t.Fatalf("kill count = %d, want 1", process.killCount())
	}
}

func TestDevRejectsInvalidOptions(t *testing.T) {
	root := devProjectRoot(t)
	harness := newDevHarness()
	command := devCommand{system: harness.system()}
	base := devOptions{
		root: root, device: "chrome", transport: devTransportHTTP,
		listen: "127.0.0.1:8080", token: "token", corsOrigin: "*",
	}
	tests := []struct {
		name   string
		change func(*devOptions)
	}{
		{name: "unknown transport", change: func(options *devOptions) {
			options.transport = "magic"
		}},
		{name: "invalid listen address", change: func(options *devOptions) {
			options.listen = "localhost"
		}},
		{name: "TLS URL for local HTTP server", change: func(options *devOptions) {
			options.backendURL = "https://example.test/rpc"
		}},
		{name: "wrong endpoint", change: func(options *devOptions) {
			options.backendURL = "http://example.test/api"
		}},
		{name: "query string", change: func(options *devOptions) {
			options.backendURL = "http://127.0.0.1:8080/rpc?debug=true"
		}},
		{name: "mismatched backend port", change: func(options *devOptions) {
			options.backendURL = "http://127.0.0.1:9090/rpc"
		}},
		{name: "LAN URL with loopback listener", change: func(options *devOptions) {
			options.backendURL = "http://192.0.2.10:8080/rpc"
		}},
		{name: "empty token", change: func(options *devOptions) {
			options.token = " "
		}},
		{name: "cross-host sidecar", change: func(options *devOptions) {
			options.transport = devTransportSidecar
			options.device = "macos"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := base
			test.change(&options)
			if _, err := command.resolveOptions(options); !errors.Is(err, errDevInvalid) {
				t.Fatalf("error = %v, want errDevInvalid", err)
			}
		})
	}
}

func TestDevRequiresReachableURLForMobileDevice(t *testing.T) {
	root := devProjectRoot(t)
	command := devCommand{system: newDevHarness().system()}
	_, err := command.resolveOptions(devOptions{
		root:       root,
		device:     "emulator-5554",
		transport:  devTransportHTTP,
		listen:     "0.0.0.0:8080",
		token:      "token",
		corsOrigin: "*",
	})
	if !errors.Is(err, errDevInvalid) || !strings.Contains(err.Error(), "--backend-url") {
		t.Fatalf("error = %v, want reachable backend URL requirement", err)
	}
}

func TestDevRejectsInvalidProjectMetadata(t *testing.T) {
	root := makeProjectRoot(t, validProjectMetadata+"{}")
	command := devCommand{system: newDevHarness().system()}
	_, err := command.resolveOptions(devOptions{root: root})
	if !errors.Is(err, errDevInvalid) || !errors.Is(err, errProjectInvalid) {
		t.Fatalf("error = %v, want dev and project errors", err)
	}
}

func TestDevDefaultsWebDeviceToLocalHTTPBackend(t *testing.T) {
	root := devProjectRoot(t)
	command := devCommand{system: newDevHarness().system()}
	options, err := command.resolveOptions(devOptions{
		root:       root,
		device:     "chrome",
		transport:  devTransportAuto,
		listen:     "127.0.0.1:8080",
		token:      "token",
		corsOrigin: "*",
	})
	if err != nil {
		t.Fatalf("resolve options: %v", err)
	}
	if options.transport != devTransportHTTP || options.backendURL != "http://127.0.0.1:8080/rpc" {
		t.Fatalf("options = %#v", options)
	}
}

func TestLocalReadyAddress(t *testing.T) {
	tests := map[string]string{
		"127.0.0.1:8080": "127.0.0.1:8080",
		"0.0.0.0:9090":   "127.0.0.1:9090",
		"[::]:7070":      "127.0.0.1:7070",
	}
	for input, want := range tests {
		got, err := localReadyAddress(input)
		if err != nil || got != want {
			t.Fatalf("localReadyAddress(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
}

type fakeDevProcess struct {
	mutex      sync.Mutex
	wait       chan error
	finishOnce sync.Once
	autoExit   bool
	signals    []os.Signal
	kills      int
}

func newFakeDevProcess(autoExit bool) *fakeDevProcess {
	return &fakeDevProcess{wait: make(chan error, 1), autoExit: autoExit}
}

func (process *fakeDevProcess) Wait() error {
	return <-process.wait
}

func (process *fakeDevProcess) Signal(value os.Signal) error {
	process.mutex.Lock()
	process.signals = append(process.signals, value)
	autoExit := process.autoExit
	process.mutex.Unlock()
	if autoExit {
		process.finish(nil)
	}
	return nil
}

func (process *fakeDevProcess) Kill() error {
	process.mutex.Lock()
	process.kills++
	process.mutex.Unlock()
	process.finish(nil)
	return nil
}

func (process *fakeDevProcess) finish(err error) {
	process.finishOnce.Do(func() {
		process.wait <- err
	})
}

func (process *fakeDevProcess) receivedSignals() []os.Signal {
	process.mutex.Lock()
	defer process.mutex.Unlock()
	return append([]os.Signal(nil), process.signals...)
}

func (process *fakeDevProcess) killCount() int {
	process.mutex.Lock()
	defer process.mutex.Unlock()
	return process.kills
}

type devHarness struct {
	mutex         sync.Mutex
	processes     []*fakeDevProcess
	runs          []devProcessSpec
	starts        []devProcessSpec
	startErrors   map[int]error
	ready         []string
	readyError    error
	notifications chan os.Signal
	afterStart    func(int)
}

func newDevHarness(processes ...*fakeDevProcess) *devHarness {
	return &devHarness{
		processes:     processes,
		startErrors:   map[int]error{},
		notifications: make(chan os.Signal, 1),
	}
}

func (harness *devHarness) system() devSystem {
	return devSystem{
		goos:         "linux",
		stdin:        strings.NewReader(""),
		readyTimeout: time.Second,
		stopTimeout:  50 * time.Millisecond,
		abs:          filepath.Abs,
		stat:         os.Stat,
		mkdirAll:     os.MkdirAll,
		run: func(specification devProcessSpec) error {
			harness.mutex.Lock()
			defer harness.mutex.Unlock()
			harness.runs = append(harness.runs, specification)
			return nil
		},
		start: func(specification devProcessSpec) (devProcess, error) {
			harness.mutex.Lock()
			index := len(harness.starts)
			harness.starts = append(harness.starts, specification)
			startError := harness.startErrors[index]
			var process *fakeDevProcess
			if index < len(harness.processes) {
				process = harness.processes[index]
			}
			afterStart := harness.afterStart
			harness.mutex.Unlock()
			if startError != nil {
				return nil, startError
			}
			if process == nil {
				return nil, errors.New("unexpected process start")
			}
			if afterStart != nil {
				afterStart(index)
			}
			return process, nil
		},
		waitReady: func(address string, _ time.Duration) error {
			harness.mutex.Lock()
			defer harness.mutex.Unlock()
			harness.ready = append(harness.ready, address)
			return harness.readyError
		},
		signals: func() (<-chan os.Signal, func()) {
			return harness.notifications, func() {}
		},
	}
}

func (harness *devHarness) specifications() ([]devProcessSpec, []devProcessSpec) {
	harness.mutex.Lock()
	defer harness.mutex.Unlock()
	return append([]devProcessSpec(nil), harness.runs...),
		append([]devProcessSpec(nil), harness.starts...)
}

func (harness *devHarness) readyAddresses() []string {
	harness.mutex.Lock()
	defer harness.mutex.Unlock()
	return append([]string(nil), harness.ready...)
}

func devProjectRoot(t *testing.T) string {
	t.Helper()
	root := makeProjectRoot(t, validProjectMetadata)
	for path, contents := range map[string]string{
		".fvmrc":       "{\n  \"flutter\": \"3.44.6\"\n}\n",
		"pubspec.yaml": "name: example\n",
	} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root
}

func specificationCommand(specification devProcessSpec) string {
	return strings.Join(
		append([]string{specification.Name}, specification.Arguments...),
		" ",
	)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
