package wails

import (
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"trevorbuild/internal/config"
)

func Configure(project string, cfg config.Config) error {
	if err := configureMainGo(filepath.Join(project, "main.go"), cfg); err != nil {
		return err
	}
	if err := configureBuildConfig(filepath.Join(project, "build", "config.yml"), cfg); err != nil {
		return err
	}
	if cfg.Platform == "android" {
		return configureAndroid(project, cfg)
	}
	return nil
}

func configureMainGo(path string, cfg config.Config) error {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse generated main.go: %w", err)
	}
	foundOptions, foundWindow := false, false
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		switch compositeTypeName(literal.Type) {
		case "Options":
			foundOptions = true
			setStringField(literal, "Name", cfg.AppName)
			setStringField(literal, "Description", cfg.Description)
		case "WebviewWindowOptions":
			foundWindow = true
			setStringField(literal, "Title", cfg.Window.Title)
			setIntField(literal, "Width", cfg.Window.Width)
			setIntField(literal, "Height", cfg.Window.Height)
			if cfg.Window.Resizable != nil {
				setBoolField(literal, "DisableResize", !*cfg.Window.Resizable)
			}
		}
		return true
	})
	if !foundOptions {
		return errors.New("could not find Wails application.Options in generated main.go; template may have changed")
	}
	if !foundWindow {
		return errors.New("could not find Wails WebviewWindowOptions in generated main.go; template may have changed")
	}
	var output strings.Builder
	if err := format.Node(&output, fileSet, file); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(output.String()), 0640)
}

func compositeTypeName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.SelectorExpr:
		return typed.Sel.Name
	case *ast.Ident:
		return typed.Name
	default:
		return ""
	}
}

func setStringField(literal *ast.CompositeLit, name, value string) {
	setCompositeField(literal, name, &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(value)})
}

func setIntField(literal *ast.CompositeLit, name string, value int) {
	setCompositeField(literal, name, &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(value)})
}

func setBoolField(literal *ast.CompositeLit, name string, value bool) {
	setCompositeField(literal, name, ast.NewIdent(strconv.FormatBool(value)))
}

func setCompositeField(literal *ast.CompositeLit, name string, value ast.Expr) {
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		identifier, ok := field.Key.(*ast.Ident)
		if ok && identifier.Name == name {
			field.Value = value
			return
		}
	}
	literal.Elts = append(literal.Elts, &ast.KeyValueExpr{Key: ast.NewIdent(name), Value: value})
}

func configureBuildConfig(path string, cfg config.Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read generated Wails build config: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	inInfo := false
	updated := map[string]bool{}
	values := map[string]string{
		"productName":       cfg.AppName,
		"productIdentifier": cfg.AppID,
		"description":       cfg.Description,
		"version":           cfg.Version,
	}
	if cfg.Company != "" {
		values["companyName"] = cfg.Company
	}
	keyPattern := regexp.MustCompile(`^(\s*)([A-Za-z][A-Za-z0-9_]*):.*$`)
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "info:" {
			inInfo = true
			continue
		}
		if inInfo && len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
			inInfo = false
		}
		if !inInfo {
			continue
		}
		match := keyPattern.FindStringSubmatch(line)
		if len(match) != 3 {
			continue
		}
		if value, ok := values[match[2]]; ok {
			lines[index] = match[1] + match[2] + ": " + strconv.Quote(value)
			updated[match[2]] = true
		}
	}
	for key := range values {
		if !updated[key] {
			return fmt.Errorf("generated build/config.yml is missing info.%s", key)
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0640)
}

func configureAndroid(project string, cfg config.Config) error {
	gradlePath := filepath.Join(project, "build", "android", "app", "build.gradle")
	gradle, err := os.ReadFile(gradlePath)
	if err != nil {
		return fmt.Errorf("read generated Android app/build.gradle: %w", err)
	}
	gradleText := string(gradle)
	namespacePattern := regexp.MustCompile(`(?m)^\s*namespace\s+['"]([^'"]+)['"]`)
	namespace := namespacePattern.FindStringSubmatch(gradleText)
	if len(namespace) != 2 || namespace[1] != "com.wails.app" {
		return errors.New("generated Android Java namespace must remain com.wails.app")
	}
	applicationPattern := regexp.MustCompile(`(?m)^(\s*applicationId\s+)["'][^"']+["']`)
	if !applicationPattern.MatchString(gradleText) {
		return errors.New("generated Android build.gradle is missing applicationId")
	}
	gradleText = applicationPattern.ReplaceAllString(gradleText, `${1}"`+cfg.AppID+`"`)
	versionPattern := regexp.MustCompile(`(?m)^(\s*versionName\s+)["'][^"']+["']`)
	if versionPattern.MatchString(gradleText) {
		gradleText = versionPattern.ReplaceAllString(gradleText, `${1}"`+cfg.Version+`"`)
	}
	if err := os.WriteFile(gradlePath, []byte(gradleText), 0640); err != nil {
		return err
	}

	stringsPath := filepath.Join(project, "build", "android", "app", "src", "main", "res", "values", "strings.xml")
	resource, err := os.ReadFile(stringsPath)
	if err != nil {
		return fmt.Errorf("read generated Android strings.xml: %w", err)
	}
	appNamePattern := regexp.MustCompile(`(?s)(<string\s+name="app_name"[^>]*>).*?(</string>)`)
	if !appNamePattern.MatchString(string(resource)) {
		return errors.New("generated Android strings.xml is missing app_name")
	}
	escaped := xmlEscape(cfg.AppName)
	updated := appNamePattern.ReplaceAllString(string(resource), `${1}`+escaped+`${2}`)
	return os.WriteFile(stringsPath, []byte(updated), 0640)
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return replacer.Replace(value)
}
