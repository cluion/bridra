package webdist

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, data string) {
	t.Helper()
	target := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPackageRecordsFileAndTreeSHA256AndReusesCompleteSnapshot(t *testing.T) {
	root := t.TempDir()
	const dist = "frontend/dist"
	const wantDigest = "e123711c05931d66d0e6fc9035c6514991a0c0d2c078df5189bbac76ea2da284"
	write(t, root, dist+"/index.html", "<main>ok</main>")
	write(t, root, dist+"/assets/a.js", "中文")
	results := make(chan error, 3)
	for range cap(results) {
		go func() {
			hash, err := Package(root, dist)
			if err == nil && hash != wantDigest {
				t.Errorf("tree SHA-256 = %s", hash)
			}
			results <- err
		}()
	}
	for range cap(results) {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	published := filepath.Join(root, OutputDirectory, wantDigest)
	b, err := os.ReadFile(filepath.Join(published, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	wantFiles := map[string]string{
		"assets/a.js": "72726d8818f693066ceb69afa364218b692e62ea92b385782363780f47529c21",
		"index.html":  "8100bd57039153d036e17426b3eca44441e3e58ad4aa21bb3ba55ac8734a3cc4",
	}
	if m.ArtifactSHA256 != wantDigest || m.SchemaVersion != 1 || m.Artifact != "dist" || !maps.Equal(m.Files, wantFiles) {
		t.Fatalf("unexpected manifest: %s", b)
	}
	write(t, root, dist+"/index.html", "a new build")
	newDigest, err := Package(root, dist)
	if err != nil || newDigest == wantDigest {
		t.Fatalf("second build returned %q, %v", newDigest, err)
	}
	oldIndex, err := os.ReadFile(filepath.Join(published, "dist", "index.html"))
	if err != nil || string(oldIndex) != "<main>ok</main>" {
		t.Fatalf("previous package changed: %q, %v", oldIndex, err)
	}
	write(t, root, dist+"/index.html", "<main>ok</main>")
	write(t, published, "dist/assets/a.js", "corrupt package")
	if _, err := Package(root, dist); err == nil {
		t.Fatal("accepted a corrupt previously published snapshot")
	}
}

func TestPackageRejectsSymlinkInputAndOutput(t *testing.T) {
	for _, link := range []string{"frontend", "frontend/dist", "frontend/dist/index.html", "frontend/dist/leak.js", "build", "build/bridra/web"} {
		t.Run(link, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			write(t, root, "frontend/dist/index.html", "ok")
			write(t, outside, "marker", "private")
			path := filepath.Join(root, link)
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, path); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			if _, err := Package(root, "frontend/dist"); err == nil {
				t.Fatal("accepted a symlink")
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 1 {
				t.Fatalf("output escaped the project: %v, %v", entries, err)
			}
		})
	}
}

func TestPackageRequiresBuiltEntryPoint(t *testing.T) {
	root := t.TempDir()
	write(t, root, "frontend/dist/index.js", "hello")
	if _, err := Package(root, "frontend/dist"); err == nil {
		t.Fatal("accepted dist without index.html")
	}
	if _, err := os.Stat(filepath.Join(root, "build")); !os.IsNotExist(err) {
		t.Fatalf("incomplete build created outputs: %v", err)
	}
}
