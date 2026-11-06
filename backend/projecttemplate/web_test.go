package projecttemplate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cluion/bridra/backend/codegen"
)

func TestWebStartersRenderWithoutFlutter(t *testing.T) {
	for _, ui := range []string{"vue", "react"} {
		t.Run(ui, func(t *testing.T) {
			root := t.TempDir()
			config := testConfig(t)
			config.UI = ui
			config.DisplayName = "顧客 & </title><script>oops</script> {{.Secret}}"
			config.BridraFlutterVersion, config.BridraFlutterPath, config.BridraDartImport = "", "", ""
			config.FlutterVersion, config.BridraFlutterPackage = "", ""
			if err := Render(root, config); err != nil {
				t.Fatal(err)
			}
			source := func(name string) []byte { return readProjectTemplateTestFile(t, filepath.Join(root, name)) }
			for _, name := range []string{"pubspec.yaml", ".fvmrc", "lib", "backend/cmd/sidecar", "backend/mobilebridge"} {
				if _, err := os.Stat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("unexpected Flutter path %s: %v", name, err)
				}
			}
			for _, name := range []string{"backend/cmd/server/main.go", "schema/bridra.baseline.json", codegen.TypeScriptClientPath} {
				source(name)
			}
			index := string(source("frontend/index.html"))
			if !strings.Contains(index, "<title>顧客 &amp; &lt;/title&gt;&lt;script&gt;oops&lt;/script&gt; {{.Secret}}</title>") {
				t.Fatalf("display name escaped incorrectly: %s", index)
			}
			if !strings.Contains(string(source("README.md")), "bridra build web") {
				t.Fatal("web starter missing packaging instructions")
			}
			var pkg struct{ Dependencies map[string]string }
			var lock struct{ Packages map[string]json.RawMessage }
			if err := json.Unmarshal(source("frontend/package.json"), &pkg); err != nil || pkg.Dependencies[ui] == "" {
				t.Fatalf("invalid %s package: %v", ui, err)
			}
			if err := json.Unmarshal(source("frontend/package-lock.json"), &lock); err != nil || lock.Packages["node_modules/"+ui] == nil {
				t.Fatalf("invalid %s lockfile: %v", ui, err)
			}
			var meta struct {
				SchemaVersion int
				Platforms     []string
				UI            struct{ Mode, Preset, Dist string }
			}
			if err := json.Unmarshal(source(".bridra/project.json"), &meta); err != nil {
				t.Fatal(err)
			}
			if meta.SchemaVersion != 4 || meta.UI.Mode != "web" || meta.UI.Preset != ui || meta.UI.Dist != "frontend/dist" ||
				len(meta.Platforms) != 1 || meta.Platforms[0] != "web" {
				t.Fatalf("unexpected web layout: %+v", meta)
			}
		})
	}
}

func TestDesktopOnlyWebStarterDescribesAvailableBuild(t *testing.T) {
	root := t.TempDir()
	config := testConfig(t)
	config.UI = "react"
	config.Platforms = []string{"windows"}
	if err := Render(root, config); err != nil {
		t.Fatal(err)
	}
	readme := string(readProjectTemplateTestFile(t, filepath.Join(root, "README.md")))
	if strings.Contains(readme, "bridra build web") || !strings.Contains(readme, "npm run build") {
		t.Fatal("desktop-only web starter must document the frontend build and omit unsupported packaging instructions")
	}
}
