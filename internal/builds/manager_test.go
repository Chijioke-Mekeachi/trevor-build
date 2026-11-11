package builds

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"trevorbuild/internal/config"
)

func TestCancelStopsFrontendBuildAndCleansWorkspace(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0750); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "npm-started")
	childPIDPath := filepath.Join(root, "sleep.pid")
	script := "#!/bin/sh\n" +
		"echo started > \"$FRONTEND_TEST_MARKER\"\n" +
		"sleep 30 &\n" +
		"child=$!\n" +
		"echo \"$child\" > \"$FRONTEND_TEST_PID\"\n" +
		"wait \"$child\"\n"
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(script), 0750); err != nil {
		t.Fatal(err)
	}
	wailsScript := `#!/bin/sh
name=""
parent=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -n) name="$2"; shift 2 ;;
    -d) parent="$2"; shift 2 ;;
    *) shift ;;
  esac
done
project="$parent/$name"
mkdir -p "$project/build" "$project/frontend"
cat > "$project/main.go" <<'EOF'
package main
type Options struct { Name, Description string }
type WebviewWindowOptions struct { Title string; Width, Height int; DisableResize bool }
func main() {
  _ = Options{Name: "Old", Description: "Old"}
  _ = WebviewWindowOptions{Title: "Old", Width: 800, Height: 600}
}
EOF
cat > "$project/build/config.yml" <<'EOF'
info:
  companyName: "My Company"
  productName: "Old"
  productIdentifier: "com.example.old"
  description: "Old"
  version: "0.1.0"
EOF
cat > "$project/frontend/package.json" <<'EOF'
{"scripts":{"build":"vite build"},"dependencies":{}}
EOF
`
	if err := os.WriteFile(filepath.Join(bin, "wails3"), []byte(wailsScript), 0750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FRONTEND_TEST_MARKER", marker)
	t.Setenv("FRONTEND_TEST_PID", childPIDPath)

	archivePath := filepath.Join(root, "project.zip")
	writeProjectArchive(t, archivePath)
	archive, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(filepath.Join(root, "data"), "http://localhost:8080", "wails3", 1)
	cfg := config.Config{
		AppName:  "CancelTest",
		AppID:    "com.example.canceltest",
		Platform: "windows",
		Frontend: config.FrontendConfig{
			Framework: "vite", PackageManager: "npm",
		},
	}
	job, err := manager.Start(cfg, archive)
	closeErr := archive.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}

	waitFor(t, 3*time.Second, func() bool {
		_, err := os.Stat(marker)
		return err == nil
	})
	cancelled, err := manager.Cancel(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != "cancelled" {
		t.Fatalf("cancel status = %q", cancelled.Status)
	}
	waitFor(t, 3*time.Second, func() bool {
		_, err := os.Stat(filepath.Join(root, "data", "jobs", job.ID))
		return errors.Is(err, os.ErrNotExist)
	})
	pidData, err := os.ReadFile(childPIDPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool {
		return !processExists(pid)
	})
	if processExists(pid) {
		t.Fatalf("cancelled build left child process %d running", pid)
	}
	if _, err := manager.Cancel(job.ID); !errors.Is(err, ErrAlreadyFinished) {
		t.Fatalf("second cancel error = %v, want ErrAlreadyFinished", err)
	}
}

func writeProjectArchive(t *testing.T, path string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	manifest, err := writer.Create("project/package.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Write([]byte(`{"scripts":{"build":"vite build"},"devDependencies":{"vite":"1"}}`)); err != nil {
		t.Fatal(err)
	}
	index, err := writer.Create("project/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Write([]byte("<html></html>")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func processExists(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}
