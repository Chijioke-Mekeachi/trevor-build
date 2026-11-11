package frontend

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type packageManifest struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Scripts         map[string]string `json:"scripts"`
	PackageManager  string            `json:"packageManager"`
}

type ProjectInfo struct {
	Root           string
	Framework      string
	PackageManager string
}

type candidate struct {
	path string
}

func LocateRoot(upload string) (string, error) {
	var packages, static []candidate
	err := filepath.WalkDir(upload, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != upload && ignoredDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			hasPackage, err := regularFile(filepath.Join(path, "package.json"))
			if err != nil {
				return err
			}
			hasIndex, err := regularFile(filepath.Join(path, "index.html"))
			if err != nil {
				return err
			}
			if hasPackage {
				packages = append(packages, candidate{path: path})
			} else if hasIndex {
				static = append(static, candidate{path: path})
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	if len(packages) > 0 {
		if len(packages) == 1 {
			return packages[0].path, nil
		}
		return "", fmt.Errorf("ZIP contains multiple frontend projects: %s", strings.Join(relativePaths(upload, candidatePaths(packages)), ", "))
	}
	if len(static) == 1 {
		return static[0].path, nil
	}
	if len(static) > 1 {
		return "", fmt.Errorf("ZIP contains multiple static sites: %s", strings.Join(relativePaths(upload, candidatePaths(static)), ", "))
	}
	return "", errors.New("could not identify frontend root; ZIP must contain a project package.json or index.html")
}

func Detect(root string) (ProjectInfo, error) {
	info := ProjectInfo{Root: root}
	hasManifest, err := regularFile(filepath.Join(root, "package.json"))
	if err != nil {
		return info, err
	}
	if !hasManifest {
		if exists, err := regularFile(filepath.Join(root, "index.html")); err != nil {
			return info, err
		} else if exists {
			info.Framework = "html"
			return info, nil
		}
		return info, errors.New("frontend root has neither package.json nor index.html")
	}
	manifest, err := readManifest(root)
	if err != nil {
		return info, err
	}
	info.Framework = detectFramework(root, manifest)
	if info.Framework == "" {
		return info, errors.New("could not detect a supported frontend framework; set frontend.framework explicitly")
	}
	info.PackageManager, err = detectPackageManager(root, manifest)
	if err != nil {
		return info, err
	}
	return info, nil
}

func readManifest(root string) (packageManifest, error) {
	var manifest packageManifest
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, fmt.Errorf("invalid package.json: %w", err)
	}
	return manifest, nil
}

func detectFramework(root string, manifest packageManifest) string {
	dependencies := make(map[string]string, len(manifest.Dependencies)+len(manifest.DevDependencies))
	for name, version := range manifest.Dependencies {
		dependencies[name] = version
	}
	for name, version := range manifest.DevDependencies {
		dependencies[name] = version
	}
	switch {
	case hasDependency(dependencies, "@angular/cli") || hasDependency(dependencies, "@angular/core"):
		return "angular"
	case hasDependency(dependencies, "next"):
		return "nextjs-static"
	case hasDependency(dependencies, "vite") || hasViteConfig(root):
		switch {
		case hasDependency(dependencies, "react") || hasDependency(dependencies, "react-dom"):
			return "react-vite"
		case hasDependency(dependencies, "vue"):
			return "vue-vite"
		case hasDependency(dependencies, "svelte"):
			return "svelte-vite"
		default:
			return "vite"
		}
	default:
		return ""
	}
}

func detectPackageManager(root string, manifest packageManifest) (string, error) {
	lockfiles := map[string][]string{
		"npm":  {"package-lock.json", "npm-shrinkwrap.json"},
		"pnpm": {"pnpm-lock.yaml"},
		"yarn": {"yarn.lock"},
	}
	var detected []string
	for manager, names := range lockfiles {
		for _, name := range names {
			exists, err := regularFile(filepath.Join(root, name))
			if err != nil {
				return "", err
			}
			if exists {
				detected = append(detected, manager)
				break
			}
		}
	}
	sort.Strings(detected)
	if len(detected) > 1 {
		return "", fmt.Errorf("multiple package-manager lockfiles found (%s); remove the extras or set frontend.packageManager", strings.Join(detected, ", "))
	}
	if len(detected) == 1 {
		return detected[0], nil
	}
	if manager, _, ok := strings.Cut(manifest.PackageManager, "@"); ok {
		if manager == "npm" || manager == "pnpm" || manager == "yarn" {
			return manager, nil
		}
	}
	return "npm", nil
}

func hasDependency(dependencies map[string]string, name string) bool {
	_, ok := dependencies[name]
	return ok
}

func hasViteConfig(root string) bool {
	for _, extension := range []string{".js", ".mjs", ".ts", ".mts", ".cjs", ".cts"} {
		if exists, _ := regularFile(filepath.Join(root, "vite.config"+extension)); exists {
			return true
		}
	}
	return false
}

func regularFile(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("symbolic links are not allowed: %s", path)
	}
	return info.Mode().IsRegular(), nil
}

func ignoredDirectory(name string) bool {
	switch strings.ToLower(name) {
	case "__macosx", ".git", ".svn", ".hg", "node_modules", "dist", "build", "out", ".next", ".angular", ".cache", "coverage":
		return true
	default:
		return false
	}
}

func candidatePaths(candidates []candidate) []string {
	result := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		result = append(result, candidate.path)
	}
	return result
}

func relativePaths(base string, paths []string) []string {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		relative, err := filepath.Rel(base, path)
		if err == nil {
			result = append(result, relative)
		}
	}
	return result
}
