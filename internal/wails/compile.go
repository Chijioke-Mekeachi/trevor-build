package wails

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"trevorbuild/internal/config"
	"trevorbuild/internal/processutil"
)

func Compile(ctx context.Context, binary, project string, cfg config.Config, packageManager, expectedIndexPath string) (string, error) {
	var args []string
	extraEnv := []string{"APP_ID=" + cfg.AppID, "APP_NAME=" + cfg.AppName}
	if packageManager != "" {
		extraEnv = append(extraEnv, "PACKAGE_MANAGER="+packageManager)
	}
	if cfg.Platform == "android" {
		androidEnv, err := androidBuildEnvironment()
		if err != nil {
			return "", err
		}
		extraEnv = append(extraEnv, androidEnv...)
		args = []string{"task", "android:package"}
	} else {
		args = []string{"build", "GOOS=windows", "GOARCH=amd64"}
	}
	if err := processutil.Run(ctx, project, binary, extraEnv, args...); err != nil {
		return "", fmt.Errorf("Wails %s build failed: %w", cfg.Platform, err)
	}
	artifact, err := findArtifact(project, cfg.Platform)
	if err != nil {
		return "", err
	}
	if cfg.Platform == "android" {
		if err := validateAPK(artifact, expectedIndexPath); err != nil {
			return "", err
		}
	} else if err := validateEXE(artifact); err != nil {
		return "", err
	}
	return artifact, nil
}

func androidBuildEnvironment() ([]string, error) {
	sdk := os.Getenv("ANDROID_HOME")
	if sdk == "" {
		sdk = os.Getenv("ANDROID_SDK_ROOT")
	}
	if sdk == "" {
		sdk = "/opt/android-sdk"
	}
	if info, err := os.Stat(sdk); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("Android SDK not found at %q; set ANDROID_HOME or ANDROID_SDK_ROOT", sdk)
	}
	ndk := os.Getenv("ANDROID_NDK_HOME")
	if ndk == "" {
		ndk = filepath.Join(sdk, "ndk", "26.3.11579264")
	}
	compiler := filepath.Join(ndk, "toolchains", "llvm", "prebuilt", "linux-x86_64", "bin", "aarch64-linux-android21-clang")
	if info, err := os.Stat(compiler); err != nil || info.IsDir() {
		return nil, fmt.Errorf("Android NDK compiler not found at %q; set ANDROID_NDK_HOME to an installed Linux NDK", compiler)
	}
	if info, err := os.Stat(ndk); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("Android NDK not found at %q", ndk)
	}
	return []string{
		"ANDROID_HOME=" + sdk,
		"ANDROID_SDK_ROOT=" + sdk,
		"ANDROID_NDK_HOME=" + ndk,
		"CGO_ENABLED=1",
		"GOOS=android",
		"GOARCH=arm64",
		"CC=" + compiler,
		"CXX=" + strings.TrimSuffix(compiler, "-clang") + "-clang++",
	}, nil
}

func findArtifact(project, platform string) (string, error) {
	extension := ".apk"
	if platform == "windows" {
		extension = ".exe"
	}
	var candidates []string
	err := filepath.WalkDir(project, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), extension) {
			candidates = append(candidates, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("Wails build completed but no %s artifact was found", strings.TrimPrefix(extension, "."))
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, leftErr := os.Stat(candidates[i])
		right, rightErr := os.Stat(candidates[j])
		if leftErr != nil || rightErr != nil {
			return candidates[i] < candidates[j]
		}
		return left.ModTime().After(right.ModTime())
	})
	return candidates[0], nil
}

func validateAPK(path, expectedIndexPath string) error {
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return errors.New("Android build did not produce a non-empty APK")
	}
	reader, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("Android output is not a valid APK: %w", err)
	}
	defer reader.Close()
	for _, file := range reader.File {
		if file.Name == "lib/arm64-v8a/libwails.so" {
			if file.UncompressedSize64 == 0 {
				return errors.New("APK contains an empty ARM64 Wails native library")
			}
			native, err := file.Open()
			if err != nil {
				return err
			}
			nativeData, readErr := io.ReadAll(io.LimitReader(native, 256<<20+1))
			closeErr := native.Close()
			if readErr != nil {
				return readErr
			}
			if closeErr != nil {
				return closeErr
			}
			if len(nativeData) > 256<<20 {
				return errors.New("APK native library exceeds the validation size limit")
			}
			expected, err := os.ReadFile(expectedIndexPath)
			if err != nil {
				return fmt.Errorf("read embedded frontend index.html: %w", err)
			}
			if len(expected) == 0 {
				return errors.New("frontend index.html is empty")
			}
			marker := expected
			if len(marker) > 1<<20 {
				marker = marker[:1<<20]
			}
			if !bytes.Contains(nativeData, marker) {
				return errors.New("APK native library does not contain the uploaded frontend index.html")
			}
			return nil
		}
	}
	return errors.New("APK is missing lib/arm64-v8a/libwails.so; refusing to report an incomplete Android build")
}

func validateEXE(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	header := make([]byte, 2)
	if _, err := io.ReadFull(file, header); err != nil || !bytes.Equal(header, []byte("MZ")) {
		return errors.New("Windows build output is not a valid PE executable")
	}
	return nil
}
