package frontend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trevorbuild/internal/config"
	"trevorbuild/internal/processutil"
)

type ProgressFunc func(stage string, percent int, message string)

type BuildResult struct {
	StaticDirectory string
	Framework       string
	PackageManager  string
}

func Build(ctx context.Context, root string, cfg config.FrontendConfig, progress ProgressFunc) (BuildResult, error) {
	result := BuildResult{}
	framework := cfg.Framework
	manager := cfg.PackageManager
	manifestExists, err := regularFile(filepath.Join(root, "package.json"))
	if err != nil {
		return result, err
	}

	var manifest packageManifest
	if manifestExists {
		manifest, err = readManifest(root)
		if err != nil {
			return result, err
		}
		if framework == "" {
			framework = detectFramework(root, manifest)
			if framework == "" {
				return result, errors.New("could not detect a supported frontend framework; set frontend.framework explicitly")
			}
		}
		if manager == "" {
			manager, err = detectPackageManager(root, manifest)
			if err != nil {
				return result, err
			}
		}
	} else if framework == "" {
		framework = "html"
	}
	result.Framework = framework
	result.PackageManager = manager

	outputDirectory := cfg.OutputDirectory
	if outputDirectory == "" {
		switch framework {
		case "nextjs-static":
			outputDirectory = "out"
		case "html", "static":
			outputDirectory = "."
		default:
			outputDirectory = "dist"
		}
	}
	outputPath, err := safeOutputPath(root, outputDirectory)
	if err != nil {
		return result, err
	}

	if framework != "html" && framework != "static" {
		if !manifestExists {
			return result, errors.New("selected framework requires package.json at the frontend root")
		}
		if manifest.Scripts["build"] == "" {
			return result, errors.New("package.json must define a build script")
		}
		args, err := installArgs(root, manager)
		if err != nil {
			return result, err
		}
		if progress != nil {
			progress("frontend-dependencies", 20, "Installing frontend dependencies with "+manager)
		}
		if err := processutil.Run(ctx, root, manager, nil, args...); err != nil {
			return result, fmt.Errorf("frontend dependency installation failed: %w", err)
		}
		if progress != nil {
			progress("frontend-build", 32, "Building frontend")
		}
		if err := processutil.Run(ctx, root, manager, nil, "run", "build"); err != nil {
			return result, fmt.Errorf("frontend build failed: %w", err)
		}
	}

	if framework == "angular" {
		outputPath, err = angularOutput(outputPath)
		if err != nil {
			return result, err
		}
	}
	indexPath := filepath.Join(outputPath, "index.html")
	indexInfo, err := os.Lstat(indexPath)
	if err != nil || !indexInfo.Mode().IsRegular() {
		return result, fmt.Errorf("build output %q does not contain a regular index.html; check frontend.outputDirectory and framework settings", outputDirectory)
	}
	if framework == "nextjs-static" {
		nextAssets, err := os.Lstat(filepath.Join(outputPath, "_next"))
		if err != nil || !nextAssets.IsDir() {
			return result, errors.New("Next.js export is missing _next assets; configure next.config.js with output: 'export'")
		}
	}
	result.StaticDirectory = outputPath
	return result, nil
}

func installArgs(root, manager string) ([]string, error) {
	switch manager {
	case "npm":
		for _, lock := range []string{"package-lock.json", "npm-shrinkwrap.json"} {
			exists, err := regularFile(filepath.Join(root, lock))
			if err != nil {
				return nil, err
			}
			if exists {
				return []string{"ci", "--no-audit", "--no-fund"}, nil
			}
		}
		return []string{"install", "--no-audit", "--no-fund"}, nil
	case "pnpm":
		exists, err := regularFile(filepath.Join(root, "pnpm-lock.yaml"))
		if err != nil {
			return nil, err
		}
		if exists {
			return []string{"install", "--frozen-lockfile"}, nil
		}
		return []string{"install"}, nil
	case "yarn":
		exists, err := regularFile(filepath.Join(root, "yarn.lock"))
		if err != nil {
			return nil, err
		}
		if exists {
			return []string{"install", "--non-interactive", "--frozen-lockfile"}, nil
		}
		return []string{"install", "--non-interactive"}, nil
	default:
		return nil, fmt.Errorf("unsupported package manager %q", manager)
	}
}

func safeOutputPath(root, relative string) (string, error) {
	if filepath.IsAbs(relative) || strings.Contains(relative, `\`) || strings.Contains(relative, ":") {
		return "", errors.New("frontend output directory must be relative")
	}
	for _, component := range strings.Split(filepath.ToSlash(relative), "/") {
		if component == ".." {
			return "", errors.New("frontend output directory escapes project root")
		}
	}
	base, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	output := filepath.Join(base, filepath.Clean(relative))
	rel, err := filepath.Rel(base, output)
	if err != nil || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("frontend output directory escapes project root")
	}
	return output, nil
}

func angularOutput(output string) (string, error) {
	if info, err := os.Stat(filepath.Join(output, "index.html")); err == nil && !info.IsDir() {
		return output, nil
	}
	candidates, err := filepath.Glob(filepath.Join(output, "*", "browser", "index.html"))
	if err != nil {
		return "", err
	}
	if len(candidates) == 0 {
		candidates, err = filepath.Glob(filepath.Join(output, "*", "index.html"))
		if err != nil {
			return "", err
		}
	}
	if len(candidates) == 1 {
		return filepath.Dir(candidates[0]), nil
	}
	if len(candidates) > 1 {
		return "", errors.New("Angular output contains multiple browser projects; set frontend.outputDirectory to the intended project output")
	}
	return output, nil
}
