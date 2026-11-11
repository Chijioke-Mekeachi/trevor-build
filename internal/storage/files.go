package storage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const MaxUploadBytes int64 = 100 << 20
const MaxIconUploadBytes int64 = 10 << 20

var jobIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func ValidJobID(id string) bool {
	return jobIDPattern.MatchString(id)
}

func SaveUpload(src io.Reader, dst string) error {
	file, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(file, io.LimitReader(src, MaxUploadBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n > MaxUploadBytes {
		_ = os.Remove(dst)
		return errors.New("ZIP exceeds 100 MiB")
	}
	return nil
}

func SaveIconUpload(src io.Reader, dst string) error {
	file, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(file, io.LimitReader(src, MaxIconUploadBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(dst)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		return closeErr
	}
	if n > MaxIconUploadBytes {
		_ = os.Remove(dst)
		return errors.New("icon image exceeds the 10 MiB limit")
	}
	return nil
}

func CopyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not allowed in build output: %s", relative)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0750)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported file type in build output: %s", relative)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
			return err
		}
		return CopyFile(path, target)
	})
}

func CopyFile(src, dst string) error {
	input, err := os.Open(src)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func JoinWithin(root, relative string) (string, error) {
	if filepath.IsAbs(relative) || strings.ContainsRune(relative, '\x00') {
		return "", errors.New("path must be relative")
	}
	base, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(base, filepath.Clean(relative))
	rel, err := filepath.Rel(base, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes its root")
	}
	return joined, nil
}

func ArtifactPath(root, id, extension string) (string, error) {
	if !ValidJobID(id) {
		return "", errors.New("invalid job id")
	}
	if extension != ".apk" && extension != ".exe" {
		return "", errors.New("invalid artifact type")
	}
	return filepath.Join(root, "artifacts", id+extension), nil
}
