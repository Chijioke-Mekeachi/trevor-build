package wails

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trevorbuild/internal/config"
)

func TestIntegrateFrontendPreservesWailsFilesAndReplacesDist(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "uploaded")
	generated := filepath.Join(root, "generated", "frontend")
	static := filepath.Join(root, "production")
	for _, directory := range []string{
		filepath.Join(source, "src"), filepath.Join(source, "dist"),
		filepath.Join(generated, "dist"), filepath.Join(generated, "bindings"), static,
	} {
		if err := os.MkdirAll(directory, 0750); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(source, "package.json"), `{
	  "scripts":{"build":"vite build","dev":"vite"},
	  "dependencies":{"react":"18.0.0"}
	}`)
	writeTestFile(t, filepath.Join(source, "index.html"), "<html>development entry</html>")
	writeTestFile(t, filepath.Join(source, "src", "main.js"), "console.log('uploaded source')")
	writeTestFile(t, filepath.Join(source, "dist", "index.html"), "<html>stale build</html>")
	writeTestFile(t, filepath.Join(source, "vite.config.js"), `export default { server: { port: 5173 } };`)
	writeTestFile(t, filepath.Join(static, "index.html"), "<html><title>uploaded production</title></html>")
	writeTestFile(t, filepath.Join(static, "assets", "app.js"), "window.uploaded = true")
	writeTestFile(t, filepath.Join(generated, "package.json"), `{
	  "type":"module","scripts":{"build":"vite build","dev":"vite"},
	  "dependencies":{"@wailsio/runtime":"3.0.0"}
	}`)
	writeTestFile(t, filepath.Join(generated, "vite.config.js"), `export default { server: { port: 9245 } };`)
	writeTestFile(t, filepath.Join(generated, "bindings", "runtime.js"), "generated binding")
	writeTestFile(t, filepath.Join(generated, "dist", "index.html"), "<html>vanilla template</html>")

	if err := IntegrateFrontend(source, static, generated, "react-vite"); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, filepath.Join(generated, "dist", "index.html"), "uploaded production")
	assertFileContains(t, filepath.Join(generated, "dist", "assets", "app.js"), "uploaded = true")
	assertFileContains(t, filepath.Join(generated, "index.html"), "development entry")
	assertFileContains(t, filepath.Join(generated, "src", "main.js"), "uploaded source")
	assertFileContains(t, filepath.Join(generated, "bindings", "runtime.js"), "generated binding")
	assertFileContains(t, filepath.Join(generated, "vite.config.user.js"), "5173")
	assertFileContains(t, filepath.Join(generated, "vite.config.js"), "WAILS_VITE_PORT")
	assertFileContains(t, filepath.Join(generated, "vite.config.js"), "strictPort: true")

	var packageJSON map[string]any
	data, err := os.ReadFile(filepath.Join(generated, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &packageJSON); err != nil {
		t.Fatal(err)
	}
	dependencies := packageJSON["dependencies"].(map[string]any)
	if dependencies["@wailsio/runtime"] != "3.0.0" || dependencies["react"] != "18.0.0" {
		t.Fatalf("merged dependencies = %#v", dependencies)
	}
}

func TestConfigureUpdatesMetadataAndKeepsAndroidNamespace(t *testing.T) {
	project := t.TempDir()
	writeTestFile(t, filepath.Join(project, "main.go"), `package main
import "github.com/wailsapp/wails/v3/pkg/application"
func main() {
 app := application.New(application.Options{Name:"Old", Description:"Old description"})
 app.Window.NewWithOptions(application.WebviewWindowOptions{Title:"Old", Width:800, Height:600})
}
`)
	writeTestFile(t, filepath.Join(project, "build", "config.yml"), `info:
  companyName: "My Company"
  productName: "Old"
  productIdentifier: "com.example.old"
  description: "Old description"
  version: "0.1.0"
`)
	writeTestFile(t, filepath.Join(project, "build", "android", "app", "build.gradle"), `android {
    namespace 'com.wails.app'
    defaultConfig {
        applicationId "com.wails.app"
        versionName "0.1.0"
    }
}
`)
	writeTestFile(t, filepath.Join(project, "build", "android", "app", "src", "main", "res", "values", "strings.xml"), `<resources><string name="app_name">Old</string></resources>`)
	resizable := false
	cfg := config.Config{
		AppName:     "MyApplication",
		AppID:       "com.example.myapp",
		Version:     "2.3.4",
		Description: "Uploaded frontend",
		Company:     "TrevorBuild",
		Platform:    "android",
		Window: config.WindowConfig{
			Title: "Uploaded window", Width: 1024, Height: 768, Resizable: &resizable,
		},
	}
	if err := Configure(project, cfg); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, filepath.Join(project, "main.go"), `Name: "MyApplication"`)
	assertFileContains(t, filepath.Join(project, "main.go"), `Description: "Uploaded frontend"`)
	assertFileContains(t, filepath.Join(project, "main.go"), `DisableResize: true`)
	assertFileContains(t, filepath.Join(project, "build", "config.yml"), `productIdentifier: "com.example.myapp"`)
	assertFileContains(t, filepath.Join(project, "build", "android", "app", "build.gradle"), `namespace 'com.wails.app'`)
	assertFileContains(t, filepath.Join(project, "build", "android", "app", "build.gradle"), `applicationId "com.example.myapp"`)
	assertFileContains(t, filepath.Join(project, "build", "android", "app", "src", "main", "res", "values", "strings.xml"), "MyApplication")
}

func TestApplyIconsUpdatesPlatformResources(t *testing.T) {
	project := t.TempDir()
	generated := filepath.Join(t.TempDir(), "icons")
	writeTestFile(t, filepath.Join(project, "build", "android", "app", "src", "main", "AndroidManifest.xml"),
		`<manifest xmlns:android="http://schemas.android.com/apk/res/android"><application android:icon="@mipmap/ic_launcher" android:roundIcon="@mipmap/ic_launcher_round" /></manifest>`)
	for _, path := range []string{
		filepath.Join(generated, "appicon.png"),
		filepath.Join(generated, "windows", "icon.ico"),
	} {
		writeTestFile(t, path, filepath.Base(path))
	}
	for _, density := range androidMipmaps {
		for _, name := range []string{"ic_launcher.png", "ic_launcher_round.png"} {
			writeTestFile(t, filepath.Join(generated, "android", density, name), density+"/"+name)
		}
	}
	for _, platform := range []string{"android", "windows"} {
		if err := os.MkdirAll(filepath.Join(project, "build"), 0750); err != nil {
			t.Fatal(err)
		}
		if err := ApplyIcons(project, platform, generated); err != nil {
			t.Fatal(err)
		}
	}
	assertFileContains(t, filepath.Join(project, "build", "appicon.png"), "appicon.png")
	assertFileContains(t, filepath.Join(project, "build", "windows", "icon.ico"), "icon.ico")
	assertFileContains(t, filepath.Join(project, "build", "android", "app", "src", "main", "res", "mipmap-xxxhdpi", "ic_launcher.png"), "mipmap-xxxhdpi/ic_launcher.png")
	assertFileContains(t, filepath.Join(project, "build", "android", "app", "src", "main", "res", "mipmap-xxxhdpi", "ic_launcher_round.png"), "mipmap-xxxhdpi/ic_launcher_round.png")
}

func TestValidateAPKRequiresEmbeddedFrontendAndNativeLibrary(t *testing.T) {
	root := t.TempDir()
	apkPath := filepath.Join(root, "app.apk")
	indexPath := filepath.Join(root, "index.html")
	if err := os.WriteFile(indexPath, []byte("<html><title>uploaded application marker</title></html>"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(apkPath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	native, err := writer.Create("lib/arm64-v8a/libwails.so")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = native.Write([]byte("native-start <html><title>uploaded application marker</title></html> native-end"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := validateAPK(apkPath, indexPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, []byte("<html>wrong frontend</html>"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateAPK(apkPath, indexPath); err == nil || !strings.Contains(err.Error(), "does not contain") {
		t.Fatalf("validateAPK error = %v, want embedded frontend mismatch", err)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func assertFileContains(t *testing.T, path, text string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), text) {
		t.Fatalf("%s does not contain %q", path, text)
	}
}
