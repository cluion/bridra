package webdesktop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const webView2SDKVersion = "1.0.4314.35"

func buildWindowsNative(sourceDir, output string) error {
	arch := map[string]string{"amd64": "x64", "arm64": "arm64", "386": "x86"}[runtime.GOARCH]
	if arch == "" {
		return fmt.Errorf("unsupported Windows WebView2 architecture %s", runtime.GOARCH)
	}
	sdk := os.Getenv("BRIDRA_WEBVIEW2_SDK")
	if sdk == "" {
		cache := os.Getenv("NUGET_PACKAGES")
		if cache == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			cache = filepath.Join(home, ".nuget", "packages")
		}
		sdk = filepath.Join(cache, "microsoft.web.webview2", webView2SDKVersion)
	}
	sdk, err := filepath.Abs(sdk)
	if err != nil {
		return err
	}
	native := filepath.Join(sdk, "build", "native")
	include := filepath.Join(native, "include")
	loader := filepath.Join(native, arch, "WebView2LoaderStatic.lib")
	for _, path := range []string{filepath.Join(include, "WebView2.h"), loader} {
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("missing WebView2 SDK file %s; set BRIDRA_WEBVIEW2_SDK to the extracted Microsoft.Web.WebView2 package", path)
		}
	}
	cl, err := exec.LookPath("cl.exe")
	if err != nil {
		return fmt.Errorf("open the %s MSVC Native Tools prompt to build the WebView2 host: %w", arch, err)
	}
	cmd := exec.Command(cl, "/nologo", "/std:c++20", "/EHsc", "/utf-8", "/MT", "/O2", "/W4", "/I"+include, filepath.Join(sourceDir, "host.cpp"), "/Fo"+filepath.Join(sourceDir, "host.obj"), "/Fe"+output, "/link", "/SUBSYSTEM:WINDOWS", "/MACHINE:"+arch, loader, "user32.lib", "ole32.lib", "shell32.lib", "shlwapi.lib", "version.lib", "advapi32.lib", "oleaut32.lib", "winhttp.lib", "windowsapp.lib", "runtimeobject.lib")
	cmd.Dir = sourceDir
	if diagnostics, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("compile WebView2 host with MSVC: %w: %s", err, diagnostics)
	}
	return nil
}
