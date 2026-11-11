package wails

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trevorbuild/internal/storage"
)

func IntegrateFrontend(sourceRoot, staticDirectory, generatedFrontend, framework string) error {
	if info, err := os.Stat(filepath.Join(staticDirectory, "index.html")); err != nil || info.IsDir() {
		return errors.New("compiled frontend must contain index.html")
	}
	dist := filepath.Join(generatedFrontend, "dist")
	if err := os.RemoveAll(dist); err != nil {
		return fmt.Errorf("remove generated frontend dist: %w", err)
	}
	if err := os.MkdirAll(dist, 0750); err != nil {
		return err
	}
	if err := storage.CopyTree(staticDirectory, dist); err != nil {
		return fmt.Errorf("copy uploaded production assets: %w", err)
	}
	if err := overlayDevelopmentFiles(sourceRoot, generatedFrontend); err != nil {
		return err
	}
	if err := mergePackageJSON(sourceRoot, generatedFrontend, framework); err != nil {
		return err
	}
	return integrateViteConfig(sourceRoot, generatedFrontend)
}

func overlayDevelopmentFiles(sourceRoot, generatedFrontend string) error {
	return filepath.WalkDir(sourceRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if ignoredDevelopmentPath(relative, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("frontend development source contains a symlink: %s", relative)
		}
		target := filepath.Join(generatedFrontend, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0750)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported frontend source file type: %s", relative)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
			return err
		}
		return storage.CopyFile(path, target)
	})
}

func ignoredDevelopmentPath(relative string, isDirectory bool) bool {
	normalized := filepath.ToSlash(relative)
	components := strings.Split(normalized, "/")
	for _, component := range components {
		switch component {
		case ".git", ".svn", ".hg", "node_modules", "__MACOSX":
			return true
		}
	}
	if len(components) == 1 {
		switch components[0] {
		case "package.json", "package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock":
			return true
		}
		if isDirectory && (components[0] == "dist" || components[0] == "build" || components[0] == "out" || components[0] == ".next" || components[0] == ".angular") {
			return true
		}
	}
	if strings.HasPrefix(filepath.Base(relative), ".env") || filepath.Base(relative) == ".DS_Store" {
		return true
	}
	return false
}

func mergePackageJSON(sourceRoot, generatedFrontend, framework string) error {
	source, err := os.ReadFile(filepath.Join(sourceRoot, "package.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	generatedPath := filepath.Join(generatedFrontend, "package.json")
	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		return err
	}
	var sourcePackage, generatedPackage map[string]json.RawMessage
	if err := json.Unmarshal(source, &sourcePackage); err != nil {
		return fmt.Errorf("read uploaded package.json: %w", err)
	}
	if err := json.Unmarshal(generated, &generatedPackage); err != nil {
		return fmt.Errorf("read generated package.json: %w", err)
	}
	for _, field := range []string{"dependencies", "devDependencies", "scripts"} {
		var userValues, generatedValues map[string]json.RawMessage
		if data, ok := sourcePackage[field]; ok {
			if err := json.Unmarshal(data, &userValues); err != nil {
				return fmt.Errorf("uploaded package.json %s: %w", field, err)
			}
		}
		if data, ok := generatedPackage[field]; ok {
			if err := json.Unmarshal(data, &generatedValues); err != nil {
				return fmt.Errorf("generated package.json %s: %w", field, err)
			}
		}
		if generatedValues == nil {
			generatedValues = make(map[string]json.RawMessage)
		}
		for name, value := range userValues {
			generatedValues[name] = value
		}
		merged, err := json.Marshal(generatedValues)
		if err != nil {
			return err
		}
		generatedPackage[field] = merged
	}
	for _, field := range []string{"engines"} {
		if value, ok := sourcePackage[field]; ok {
			generatedPackage[field] = value
		}
	}
	if framework == "angular" {
		setPackageScript(generatedPackage, "dev", "ng serve --host 127.0.0.1 --port ${WAILS_VITE_PORT:-9245}")
	} else if framework == "nextjs-static" {
		setPackageScript(generatedPackage, "dev", "next dev --hostname 127.0.0.1 --port ${WAILS_VITE_PORT:-9245}")
	}
	encoded, err := json.MarshalIndent(generatedPackage, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(generatedPath, append(encoded, '\n'), 0640)
}

func setPackageScript(pkg map[string]json.RawMessage, name, value string) {
	var scripts map[string]json.RawMessage
	_ = json.Unmarshal(pkg["scripts"], &scripts)
	if scripts == nil {
		scripts = make(map[string]json.RawMessage)
	}
	scripts[name], _ = json.Marshal(value)
	pkg["scripts"], _ = json.Marshal(scripts)
}

func integrateViteConfig(sourceRoot, generatedFrontend string) error {
	var sourceConfig string
	for _, extension := range []string{".js", ".mjs", ".ts", ".mts", ".cjs", ".cts"} {
		path := filepath.Join(sourceRoot, "vite.config"+extension)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("uploaded vite.config must be a regular file")
		}
		sourceConfig = path
		break
	}
	if sourceConfig == "" {
		return nil
	}
	extension := filepath.Ext(sourceConfig)
	userConfig := "vite.config.user" + extension
	if err := storage.CopyFile(sourceConfig, filepath.Join(generatedFrontend, userConfig)); err != nil {
		return err
	}
	configBytes, err := os.ReadFile(sourceConfig)
	if err != nil {
		return err
	}
	useWailsPlugin := !strings.Contains(string(configBytes), "@wailsio/runtime/plugins/vite")
	plugin := ""
	if useWailsPlugin {
		plugin = `, plugins: [wails("./bindings")]`
	}
	wrapper := fmt.Sprintf(`import userConfig from "./%s";
import { defineConfig, mergeConfig } from "vite";
%s

export default defineConfig(async (environment) => {
  const uploaded = typeof userConfig === "function"
    ? await userConfig(environment)
    : await userConfig;
  if (Array.isArray(uploaded)) {
    throw new Error("TrevorBuild cannot merge a multi-config Vite export for Wails development mode");
  }
  return mergeConfig(uploaded || {}, {
    server: {
      host: "127.0.0.1",
      port: Number(process.env.WAILS_VITE_PORT || 9245),
      strictPort: true
    }%s
  });
});
`, userConfig, importWailsPlugin(useWailsPlugin), plugin)
	return os.WriteFile(filepath.Join(generatedFrontend, "vite.config.js"), []byte(wrapper), 0640)
}

func importWailsPlugin(enabled bool) string {
	if enabled {
		return `import wails from "@wailsio/runtime/plugins/vite";`
	}
	return ""
}
