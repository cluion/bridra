package webdesktop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func buildLinuxNative(sourceDir, output string) error {
	flags, err := exec.Command("pkg-config", "--cflags", "--libs", "gtk4", "webkitgtk-6.0", "libsoup-3.0").Output()
	if err != nil {
		return fmt.Errorf("Linux Web UI requires pkg-config and development files for GTK4, WebKitGTK 6.0 and libsoup 3: %w", err)
	}
	args := []string{"-O2", "-std=c11", "-Wall", "-Wextra", "-o", output, filepath.Join(sourceDir, "host.c")}
	// Let pkg-config perform quoting for compiler flags with spaces without invoking a shell.
	// The compiler uses the same escaping conventions for response files.
	flagFile := filepath.Join(sourceDir, "pkg-config.rsp")
	if err := os.WriteFile(flagFile, flags, 0600); err != nil {
		return err
	}
	out, err := exec.Command("cc", append(args, "@"+flagFile)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("compile Linux WebView host: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
