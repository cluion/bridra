package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

func Run(title, bearer string, backend http.Handler) (result error) {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}
	bin := filepath.Dir(exe)
	resources := bin
	native := "bridra-webview"
	if runtime.GOOS == "darwin" {
		resources = filepath.Join(bin, "..", "Resources")
	}
	if runtime.GOOS == "windows" {
		native += ".exe"
	}
	dist, err := os.OpenRoot(filepath.Join(resources, "dist"))
	if err != nil {
		return err
	}
	defer dist.Close()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	g := newGateway(listener.Addr().String(), bearer, dist, backend)
	server := &http.Server{Handler: g, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: time.Minute, IdleTimeout: time.Minute, MaxHeaderBytes: 64 << 10}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			result = errors.Join(result, err, server.Close())
		}
	}()
	profile, err := os.MkdirTemp("", "bridra-desktop-")
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, removeProfile(profile)) }()
	cmd := exec.Command(filepath.Join(bin, native))
	cmd.Dir = profile
	cmd.Env = nativeEnv()
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer input.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open native webview: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	err = json.NewEncoder(input).Encode(map[string]any{"version": 1, "origin": g.origin, "bootstrap": g.proof, "title": title})
	if err == nil {
		select {
		case err := <-exited:
			return err
		case err = <-served:
		case <-ctx.Done():
		}
	}
	input.Close()
	select {
	case childErr := <-exited:
		return errors.Join(err, childErr)
	case <-time.After(3 * time.Second):
		killErr := cmd.Process.Kill()
		return errors.Join(err, killErr, <-exited)
	}
}

// A WebView2 process may briefly hold its user-data directory after the window closes.
func removeProfile(dir string) error {
	for i := 0; i < 20; i++ {
		if err := os.RemoveAll(dir); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return os.RemoveAll(dir)
}

func nativeEnv() []string {
	keys := strings.Fields("HOME TMP TEMP TMPDIR PATH SystemRoot SystemDrive WINDIR ProgramFiles ProgramFiles(x86) ProgramW6432 ProgramData USERPROFILE LOCALAPPDATA APPDATA LANG LANGUAGE LC_ALL LC_CTYPE DISPLAY WAYLAND_DISPLAY XAUTHORITY XDG_RUNTIME_DIR XDG_CONFIG_HOME XDG_CACHE_HOME XDG_DATA_HOME XDG_DATA_DIRS XDG_SESSION_TYPE XDG_CURRENT_DESKTOP DBUS_SESSION_BUS_ADDRESS")
	var env []string
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}
