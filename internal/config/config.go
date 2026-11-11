package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
)

type Config struct {
	AppName     string         `json:"appName"`
	AppID       string         `json:"appID"`
	Icon        string         `json:"icon,omitempty"`
	Version     string         `json:"version"`
	Description string         `json:"description"`
	Company     string         `json:"company,omitempty"`
	Platform    string         `json:"platform"`
	Frontend    FrontendConfig `json:"frontend"`
	Window      WindowConfig   `json:"window"`
}

type FrontendConfig struct {
	Framework       string `json:"framework,omitempty"`
	PackageManager  string `json:"packageManager,omitempty"`
	OutputDirectory string `json:"outputDirectory,omitempty"`
}

type WindowConfig struct {
	Title     string `json:"title,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Resizable *bool  `json:"resizable,omitempty"`
}

var (
	appNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{1,59}$`)
	appIDPattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*){1,5}$`)
	versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+([+-][A-Za-z0-9.-]+)?$`)
)

var frameworks = map[string]struct{}{
	"vite": {}, "react-vite": {}, "vue-vite": {}, "svelte-vite": {},
	"angular": {}, "nextjs-static": {}, "html": {}, "static": {},
}

func Parse(raw string) (Config, error) {
	var cfg Config
	if strings.TrimSpace(raw) == "" {
		return cfg, errors.New("multipart field 'config' is required as JSON")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("invalid config JSON: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return cfg, errors.New("invalid config JSON: expected a single JSON object")
	}

	cfg.AppName = strings.TrimSpace(cfg.AppName)
	cfg.AppID = strings.TrimSpace(cfg.AppID)
	cfg.Icon = strings.TrimSpace(cfg.Icon)
	cfg.Platform = strings.ToLower(strings.TrimSpace(cfg.Platform))
	cfg.Frontend.Framework = strings.ToLower(strings.TrimSpace(cfg.Frontend.Framework))
	cfg.Frontend.PackageManager = strings.ToLower(strings.TrimSpace(cfg.Frontend.PackageManager))
	cfg.Frontend.OutputDirectory = strings.TrimSpace(cfg.Frontend.OutputDirectory)
	cfg.Window.Title = strings.TrimSpace(cfg.Window.Title)
	if cfg.AppName == "" || !appNamePattern.MatchString(cfg.AppName) {
		return cfg, errors.New("appName is required and must contain 2-60 letters, numbers, dots, underscores or hyphens")
	}
	if len(cfg.Icon) > 255 {
		return cfg, errors.New("icon filename must be at most 255 characters")
	}
	if !appIDPattern.MatchString(cfg.AppID) {
		return cfg, errors.New("appID must be a valid reverse-domain identifier, e.g. com.example.app")
	}
	if cfg.Version == "" {
		cfg.Version = "1.0.0"
	}
	if !versionPattern.MatchString(cfg.Version) {
		return cfg, errors.New("version must use semantic version format, e.g. 1.0.0")
	}
	if cfg.Platform != "android" && cfg.Platform != "windows" {
		return cfg, errors.New("platform must be android or windows")
	}
	if cfg.Frontend.Framework == "auto" {
		cfg.Frontend.Framework = ""
	}
	if cfg.Frontend.Framework != "" {
		if _, ok := frameworks[cfg.Frontend.Framework]; !ok {
			return cfg, errors.New("unsupported frontend.framework; call GET /api/v1/frameworks")
		}
	}
	if cfg.Frontend.PackageManager != "" &&
		cfg.Frontend.PackageManager != "npm" &&
		cfg.Frontend.PackageManager != "pnpm" &&
		cfg.Frontend.PackageManager != "yarn" {
		return cfg, errors.New("packageManager must be npm, pnpm or yarn")
	}
	if cfg.Frontend.OutputDirectory != "" {
		if strings.Contains(cfg.Frontend.OutputDirectory, `\`) || strings.Contains(cfg.Frontend.OutputDirectory, ":") {
			return cfg, errors.New("outputDirectory must be a relative path inside the ZIP")
		}
		for _, component := range strings.Split(filepath.ToSlash(cfg.Frontend.OutputDirectory), "/") {
			if component == ".." {
				return cfg, errors.New("outputDirectory must be a relative path inside the ZIP")
			}
		}
		clean := filepath.Clean(filepath.FromSlash(cfg.Frontend.OutputDirectory))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return cfg, errors.New("outputDirectory must be a relative path inside the ZIP")
		}
	}
	if cfg.Description == "" {
		cfg.Description = cfg.AppName
	}
	if len(cfg.Description) > 250 {
		return cfg, errors.New("description must be at most 250 characters")
	}
	if cfg.Company != "" && len(cfg.Company) > 100 {
		return cfg, errors.New("company must be at most 100 characters")
	}
	if cfg.Window.Title == "" {
		cfg.Window.Title = cfg.AppName
	}
	if len(cfg.Window.Title) > 100 {
		return cfg, errors.New("window.title must be at most 100 characters")
	}
	if cfg.Window.Width == 0 {
		cfg.Window.Width = 1200
	}
	if cfg.Window.Height == 0 {
		cfg.Window.Height = 800
	}
	if cfg.Window.Width < 320 || cfg.Window.Width > 7680 || cfg.Window.Height < 240 || cfg.Window.Height > 4320 {
		return cfg, errors.New("window dimensions are outside allowed limits")
	}
	return cfg, nil
}

func SupportedFrameworks() []map[string]string {
	return []map[string]string{
		{"id": "vite", "name": "Vite", "defaultOutput": "dist", "notes": "Vite-based projects including React, Vue and Svelte."},
		{"id": "react-vite", "name": "React + Vite", "defaultOutput": "dist", "notes": "React application built with Vite."},
		{"id": "vue-vite", "name": "Vue + Vite", "defaultOutput": "dist", "notes": "Vue application built with Vite."},
		{"id": "svelte-vite", "name": "Svelte + Vite", "defaultOutput": "dist", "notes": "Svelte application built with Vite."},
		{"id": "angular", "name": "Angular", "defaultOutput": "dist", "notes": "Angular CLI production output; browser subfolder is detected automatically."},
		{"id": "nextjs-static", "name": "Next.js (static export)", "defaultOutput": "out", "notes": "Requires output: export; server-side features are not supported in a packaged Wails frontend."},
		{"id": "html", "name": "HTML / CSS / JavaScript", "defaultOutput": ".", "notes": "Static files; index.html must be present."},
		{"id": "static", "name": "Other static frontend", "defaultOutput": ".", "notes": "Prebuilt static site containing index.html."},
	}
}
