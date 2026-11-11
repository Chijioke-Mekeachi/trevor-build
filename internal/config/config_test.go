package config

import (
	"strings"
	"testing"
)

func TestParseDefaultsAndAutomaticDetection(t *testing.T) {
	cfg, err := Parse(`{
		"appName":"MyApplication",
		"appID":"com.example.myapplication",
		"icon":"app-icon.png",
		"platform":"android",
		"frontend":{"framework":"auto"},
		"window":{"resizable":false}
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != "1.0.0" || cfg.Window.Width != 1200 || cfg.Window.Height != 800 {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
	if cfg.Frontend.Framework != "" || cfg.Description != cfg.AppName || cfg.Window.Title != cfg.AppName {
		t.Fatalf("unexpected normalized config: %#v", cfg)
	}
	if cfg.Window.Resizable == nil || *cfg.Window.Resizable {
		t.Fatal("resizable=false was not preserved")
	}
	if cfg.Icon != "app-icon.png" {
		t.Fatalf("icon filename = %q", cfg.Icon)
	}
}

func TestParseRejectsUnsafeOrUnsupportedValues(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "unsupported framework",
			raw:  `{"appName":"App","appID":"com.example.app","platform":"windows","frontend":{"framework":"webpack"}}`,
			want: "unsupported frontend.framework",
		},
		{
			name: "output traversal",
			raw:  `{"appName":"App","appID":"com.example.app","platform":"windows","frontend":{"framework":"vite","outputDirectory":"../outside"}}`,
			want: "relative path",
		},
		{
			name: "multiple JSON values",
			raw:  `{"appName":"App","appID":"com.example.app","platform":"windows"} {}`,
			want: "single JSON object",
		},
		{
			name: "unknown field",
			raw:  `{"appName":"App","appID":"com.example.app","platform":"windows","unknown":true}`,
			want: "unknown field",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse(test.raw)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Parse error = %v, want text %q", err, test.want)
			}
		})
	}
}
