package api

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trevorbuild/internal/builds"
)

func TestAPIUploadAndStatusContract(t *testing.T) {
	root := t.TempDir()
	manager := builds.NewManager(root, "https://builds.example.test", "unused-wails", 1)
	server := httptest.NewServer(NewHandler(manager))
	defer server.Close()

	frameworksResponse, err := http.Get(server.URL + "/api/v1/frameworks")
	if err != nil {
		t.Fatal(err)
	}
	if frameworksResponse.StatusCode != http.StatusOK {
		t.Fatalf("frameworks status = %d", frameworksResponse.StatusCode)
	}
	frameworksResponse.Body.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("config", `{"appName":"TestApp","appID":"com.example.testapp","platform":"windows","frontend":{"framework":"html"}}`); err != nil {
		t.Fatal(err)
	}
	archive, err := writer.CreateFormFile("file", "frontend.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write([]byte("not a ZIP")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/builds", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("upload status = %d: %s", response.StatusCode, data)
	}
	var result struct {
		Job       builds.Job `json:"job"`
		StatusURL string     `json:"statusUrl"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Job.ID == "" || result.Job.Status != "queued" {
		t.Fatalf("unexpected job response: %#v", result.Job)
	}
	if result.StatusURL != "https://builds.example.test/api/v1/builds/"+result.Job.ID {
		t.Fatalf("statusUrl = %q", result.StatusURL)
	}

	var status builds.Job
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		statusResponse, err := http.Get(server.URL + "/api/v1/builds/" + result.Job.ID)
		if err != nil {
			t.Fatal(err)
		}
		var payload struct {
			Job builds.Job `json:"job"`
		}
		decodeErr := json.NewDecoder(statusResponse.Body).Decode(&payload)
		statusResponse.Body.Close()
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		status = payload.Job
		if status.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status.Status != "failed" || status.Error == "" {
		t.Fatalf("invalid archive did not fail with a diagnostic: %#v", status)
	}
}

func TestAPIRejectsNonZipUpload(t *testing.T) {
	manager := builds.NewManager(t.TempDir(), "http://localhost:8080", "unused-wails", 1)
	server := httptest.NewServer(NewHandler(manager))
	defer server.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("config", `{"appName":"TestApp","appID":"com.example.testapp","platform":"windows","frontend":{"framework":"html"}}`)
	file, _ := writer.CreateFormFile("file", "frontend.txt")
	_, _ = file.Write([]byte("contents"))
	_ = writer.Close()
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/builds", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
}

func TestAPIValidatesOptionalIconUpload(t *testing.T) {
	manager := builds.NewManager(t.TempDir(), "http://localhost:8080", "unused-wails", 1)
	handler := NewHandler(manager)

	t.Run("accepts valid PNG", func(t *testing.T) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("config", `{"appName":"TestApp","appID":"com.example.testapp","platform":"windows","icon":"app.png","frontend":{"framework":"html"}}`)
		archive, _ := writer.CreateFormFile("file", "frontend.zip")
		_, _ = archive.Write([]byte("not a ZIP"))
		icon, _ := writer.CreateFormFile("icon", "app.png")
		if err := png.Encode(icon, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
			t.Fatal(err)
		}
		_ = writer.Close()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/builds", &body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202: %s", response.Code, response.Body.String())
		}
	})

	t.Run("rejects invalid image content", func(t *testing.T) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("config", `{"appName":"TestApp","appID":"com.example.testapp","platform":"windows"}`)
		archive, _ := writer.CreateFormFile("file", "frontend.zip")
		_, _ = archive.Write([]byte("not a ZIP"))
		icon, _ := writer.CreateFormFile("icon", "app.png")
		_, _ = icon.Write([]byte("not a PNG"))
		_ = writer.Close()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/builds", &body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "decode uploaded icon") {
			t.Fatalf("status/body = %d/%q, want 400 with decode error", response.Code, response.Body.String())
		}
	})
}

func TestAPIJobUploadLeavesWorkspacePrivate(t *testing.T) {
	root := t.TempDir()
	manager := builds.NewManager(root, "http://localhost:8080", "unused-wails", 1)
	handler := NewHandler(manager)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
	if response.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("expected public CORS compatibility header")
	}
	if _, err := os.Stat(filepath.Join(root, "jobs")); !os.IsNotExist(err) {
		t.Fatalf("health request unexpectedly created build data: %v", err)
	}
}
