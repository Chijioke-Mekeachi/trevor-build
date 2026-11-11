package frontend

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trevorbuild/internal/config"
)

func TestExtractZipRejectsTraversalAndSymlinks(t *testing.T) {
	tests := []struct {
		name string
		file *zip.FileHeader
	}{
		{name: "path traversal", file: regularHeader("../outside.txt")},
		{name: "normalized traversal", file: regularHeader("folder/../outside.txt")},
		{name: "windows path", file: regularHeader(`..\outside.txt`)},
		{name: "symlink", file: symlinkHeader("link")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			archivePath := filepath.Join(base, "upload.zip")
			writeArchive(t, archivePath, *test.file, "payload")
			destination := filepath.Join(base, "extracted")
			if err := ExtractZip(context.Background(), archivePath, destination); err == nil {
				t.Fatal("ExtractZip unexpectedly accepted unsafe entry")
			}
			if _, err := os.Stat(filepath.Join(base, "outside.txt")); !os.IsNotExist(err) {
				t.Fatalf("unsafe entry escaped extraction directory: %v", err)
			}
		})
	}
}

func TestExtractZipIgnoresMacOSMetadataAndLocateNestedProject(t *testing.T) {
	base := t.TempDir()
	archivePath := filepath.Join(base, "upload.zip")
	writeArchiveEntries(t, archivePath, map[string]string{
		"__MACOSX/._index.html":          "ignored",
		"./wrapper/project/package.json": `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"1"}}`,
		"./wrapper/project/index.html":   "<html></html>",
	})
	destination := filepath.Join(base, "extracted")
	if err := ExtractZip(context.Background(), archivePath, destination); err != nil {
		t.Fatal(err)
	}
	root, err := LocateRoot(destination)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := root, filepath.Join(destination, "wrapper", "project"); got != want {
		t.Fatalf("LocateRoot() = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(destination, "__MACOSX")); !os.IsNotExist(err) {
		t.Fatalf("__MACOSX metadata was not ignored: %v", err)
	}
	info, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if info.Framework != "vite" || info.PackageManager != "npm" {
		t.Fatalf("unexpected detection result: %#v", info)
	}
}

func TestLocateRootRejectsMultipleProjects(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one", "two"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := LocateRoot(root); err == nil || !strings.Contains(err.Error(), "multiple frontend projects") {
		t.Fatalf("LocateRoot error = %v, want ambiguity error", err)
	}
}

func TestBuildStaticFrontendAndRejectMissingIndex(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<main>uploaded</main>"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Build(context.Background(), root, config.FrontendConfig{Framework: "html"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.StaticDirectory != root || result.Framework != "html" {
		t.Fatalf("unexpected build result: %#v", result)
	}
	if err := os.Remove(filepath.Join(root, "index.html")); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(context.Background(), root, config.FrontendConfig{Framework: "html"}, nil); err == nil {
		t.Fatal("Build unexpectedly accepted static frontend without index.html")
	}
}

func TestBuildReportsFrontendCompilationFailure(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0750); err != nil {
		t.Fatal(err)
	}
	npm := filepath.Join(bin, "npm")
	script := "#!/bin/sh\nif [ \"$1\" = install ]; then exit 0; fi\necho 'fixture build failure' >&2\nexit 9\n"
	if err := os.WriteFile(npm, []byte(script), 0750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"build":"vite build"},"devDependencies":{"vite":"1"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Build(context.Background(), root, config.FrontendConfig{Framework: "vite", PackageManager: "npm"}, nil)
	if err == nil || !strings.Contains(err.Error(), "frontend build failed") || !strings.Contains(err.Error(), "fixture build failure") {
		t.Fatalf("Build error = %v, want useful build diagnostic", err)
	}
}

func regularHeader(name string) *zip.FileHeader {
	header := &zip.FileHeader{Name: name}
	header.SetMode(0600)
	return header
}

func symlinkHeader(name string) *zip.FileHeader {
	header := &zip.FileHeader{Name: name}
	header.SetMode(os.ModeSymlink | 0777)
	return header
}

func writeArchive(t *testing.T, path string, header zip.FileHeader, content string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.CreateHeader(&header)
	if err == nil {
		_, err = entry.Write([]byte(content))
	}
	closeWriterErr := writer.Close()
	closeFileErr := file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeWriterErr != nil {
		t.Fatal(closeWriterErr)
	}
	if closeFileErr != nil {
		t.Fatal(closeFileErr)
	}
}

func writeArchiveEntries(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, content := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
