package webdist

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDesktopCopiesPinnedSnapshotAndRejectsCorruption(t *testing.T) {
	p := t.TempDir()
	source := filepath.Join(p, "frontend", "dist")
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("固定輸入"), 0644); err != nil {
		t.Fatal(err)
	}
	digest, err := Package(p, "frontend/dist")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(p, OutputDirectory, digest)
	dest := t.TempDir()
	if err := CopyVerified(p, digest, dest); err != nil {
		t.Fatal(err)
	}
	if err := Verify(dest, digest); err != nil {
		t.Fatal(err)
	}
	a, err := os.ReadFile(filepath.Join(snap, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dest, "manifest.json"))
	if err != nil || string(a) != string(b) {
		t.Fatal("manifest was not copied byte for byte")
	}
	if err := os.WriteFile(filepath.Join(dest, "dist", "injected.js"), []byte("extra"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Verify(dest, digest); err == nil {
		t.Fatal("accepted extra packaged file")
	}
	if err := os.WriteFile(filepath.Join(snap, "dist", "index.html"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := CopyVerified(p, digest, t.TempDir()); err == nil {
		t.Fatal("accepted changed snapshot")
	}
	if err := CopyVerified(p, "../escape", t.TempDir()); err == nil {
		t.Fatal("accepted invalid digest")
	}
}
