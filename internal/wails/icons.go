package wails

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"trevorbuild/internal/storage"
)

var androidMipmaps = []string{
	"mipmap-mdpi",
	"mipmap-hdpi",
	"mipmap-xhdpi",
	"mipmap-xxhdpi",
	"mipmap-xxxhdpi",
}

func ApplyIcons(project, platform, generatedIcons string) error {
	buildDir := filepath.Join(project, "build")
	if platform == "windows" {
		for _, pair := range [][2]string{
			{filepath.Join(generatedIcons, "appicon.png"), filepath.Join(buildDir, "appicon.png")},
			{filepath.Join(generatedIcons, "windows", "icon.ico"), filepath.Join(buildDir, "windows", "icon.ico")},
		} {
			if err := copyGeneratedIcon(pair[0], pair[1]); err != nil {
				return err
			}
		}
		return nil
	}
	if platform != "android" {
		return fmt.Errorf("cannot apply icons for unsupported platform %q", platform)
	}
	resourceDir := filepath.Join(buildDir, "android", "app", "src", "main", "res")
	for _, density := range androidMipmaps {
		for _, name := range []string{"ic_launcher.png", "ic_launcher_round.png"} {
			source := filepath.Join(generatedIcons, "android", density, name)
			target := filepath.Join(resourceDir, density, name)
			if err := copyGeneratedIcon(source, target); err != nil {
				return err
			}
		}
	}
	return validateAndroidLauncherIconReferences(filepath.Join(buildDir, "android", "app", "src", "main", "AndroidManifest.xml"))
}

func validateAndroidLauncherIconReferences(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read generated Android manifest: %w", err)
	}
	defer file.Close()

	var iconFound, roundIconFound bool
	decoder := xml.NewDecoder(file)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("parse generated Android manifest: %w", err)
		}
		element, ok := token.(xml.StartElement)
		if !ok || element.Name.Local != "application" {
			continue
		}
		for _, attribute := range element.Attr {
			switch {
			case attribute.Name.Local == "icon" && attribute.Value == "@mipmap/ic_launcher":
				iconFound = true
			case attribute.Name.Local == "roundIcon" && attribute.Value == "@mipmap/ic_launcher_round":
				roundIconFound = true
			}
		}
	}
	if !iconFound || !roundIconFound {
		return fmt.Errorf("generated Android manifest must reference @mipmap/ic_launcher and @mipmap/ic_launcher_round")
	}
	return nil
}

func copyGeneratedIcon(source, target string) error {
	info, err := os.Stat(source)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("generated application icon is missing or not a regular file: %s", source)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
		return err
	}
	if err := storage.CopyFile(source, target); err != nil {
		return fmt.Errorf("copy application icon to %s: %w", target, err)
	}
	return nil
}
