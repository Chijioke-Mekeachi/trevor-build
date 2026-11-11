package wails

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"trevorbuild/internal/config"
	"trevorbuild/internal/processutil"
)

func Generate(ctx context.Context, binary, parent string, cfg config.Config) (string, error) {
	args := []string{
		"init",
		"-n", cfg.AppName,
		"-t", "vanilla-js",
		"-d", parent,
		"-productname", cfg.AppName,
		"-productdescription", cfg.Description,
		"-productversion", cfg.Version,
		"-productidentifier", cfg.AppID,
		"-skipgomodtidy",
	}
	if cfg.Company != "" {
		args = append(args, "-productcompany", cfg.Company)
	}
	if err := processutil.Run(ctx, "", binary, nil, args...); err != nil {
		return "", fmt.Errorf("generate Wails 3 project: %w", err)
	}
	project := filepath.Join(parent, cfg.AppName)
	if info, err := os.Stat(filepath.Join(project, "main.go")); err != nil || info.IsDir() {
		return "", errors.New("Wails template did not create the expected project main.go")
	}
	return project, nil
}
