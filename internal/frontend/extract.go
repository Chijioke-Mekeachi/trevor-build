package frontend

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	maxExpandedBytes = 500 << 20
	maxZipEntries    = 10000
)

func ExtractZip(ctx context.Context, zipPath, destination string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("invalid ZIP: %w", err)
	}
	defer reader.Close()
	if len(reader.File) > maxZipEntries {
		return errors.New("ZIP contains too many files")
	}
	root, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0750); err != nil {
		return err
	}

	var expanded int64
	for _, entry := range reader.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name
		if strings.ContainsRune(name, '\x00') || strings.Contains(name, `\`) || strings.Contains(name, ":") {
			return fmt.Errorf("unsafe ZIP entry: %q", name)
		}
		if strings.HasPrefix(name, "/") {
			return fmt.Errorf("unsafe ZIP entry: %q", name)
		}
		components := strings.Split(name, "/")
		for index, component := range components {
			if component == ".." {
				return fmt.Errorf("unsafe ZIP entry: %q", name)
			}
			if component == "" && !(index == len(components)-1 && strings.HasSuffix(name, "/")) {
				return fmt.Errorf("unsafe ZIP entry: %q", name)
			}
		}
		clean := path.Clean(name)
		if clean == "." && entry.FileInfo().IsDir() {
			continue
		}
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("unsafe ZIP entry: %q", name)
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("ZIP symlinks are not allowed: %s", name)
		}
		if skipMacOSMetadata(clean) {
			continue
		}
		target, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(clean)))
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, target)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("ZIP path escapes destination: %s", name)
		}

		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0750); err != nil {
				return fmt.Errorf("create directory %q: %w", name, err)
			}
			continue
		}
		if !entry.Mode().IsRegular() {
			return fmt.Errorf("unsupported ZIP entry type: %s", name)
		}
		if entry.UncompressedSize64 > uint64(maxExpandedBytes-expanded) {
			return errors.New("expanded ZIP exceeds 500 MiB")
		}
		expanded += int64(entry.UncompressedSize64)
		if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
			return err
		}
		if err := extractFile(ctx, entry, target); err != nil {
			return fmt.Errorf("extract %q: %w", name, err)
		}
	}
	return nil
}

func extractFile(ctx context.Context, entry *zip.File, target string) error {
	reader, err := entry.Open()
	if err != nil {
		return err
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		_ = reader.Close()
		return err
	}
	written, copyErr := io.Copy(output, io.LimitReader(contextReader{ctx: ctx, reader: reader}, int64(entry.UncompressedSize64)+1))
	closeOutputErr := output.Close()
	closeReaderErr := reader.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeOutputErr != nil {
		return closeOutputErr
	}
	if closeReaderErr != nil {
		return closeReaderErr
	}
	if uint64(written) != entry.UncompressedSize64 {
		return fmt.Errorf("declared size %d does not match extracted size %d", entry.UncompressedSize64, written)
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func skipMacOSMetadata(name string) bool {
	for _, component := range strings.Split(name, "/") {
		if component == "__MACOSX" {
			return true
		}
	}
	return false
}
