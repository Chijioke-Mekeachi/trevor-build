package builds

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"trevorbuild/internal/appicon"
	"trevorbuild/internal/config"
	"trevorbuild/internal/frontend"
	"trevorbuild/internal/storage"
	wailsbuilder "trevorbuild/internal/wails"
)

const buildTimeout = 45 * time.Minute

var (
	ErrNotFound         = errors.New("build not found")
	ErrAlreadyFinished  = errors.New("job is already finished")
	ErrArtifactNotReady = errors.New("build artifact is not ready")
)

type Job struct {
	ID          string    `json:"id"`
	Status      string    `json:"status"`
	Stage       string    `json:"stage"`
	Progress    int       `json:"progress"`
	Message     string    `json:"message,omitempty"`
	Error       string    `json:"error,omitempty"`
	Platform    string    `json:"platform"`
	Framework   string    `json:"framework"`
	AppName     string    `json:"appName"`
	DownloadURL string    `json:"downloadUrl,omitempty"`
	Artifact    string    `json:"artifact,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type record struct {
	job    Job
	config config.Config
	cancel context.CancelFunc
}

type Manager struct {
	root      string
	publicURL string
	wailsBin  string
	workers   chan struct{}
	mu        sync.RWMutex
	jobs      map[string]*record
}

func NewManager(root, publicURL, wailsBin string, workerCount int) *Manager {
	if workerCount < 1 {
		workerCount = 1
	}
	return &Manager{
		root:      root,
		publicURL: publicURL,
		wailsBin:  wailsBin,
		workers:   make(chan struct{}, workerCount),
		jobs:      make(map[string]*record),
	}
}

func (m *Manager) Start(cfg config.Config, archive io.Reader, iconUploads ...io.Reader) (Job, error) {
	if len(iconUploads) > 1 {
		return Job{}, errors.New("only one icon upload is allowed")
	}
	id, err := newID()
	if err != nil {
		return Job{}, err
	}
	jobDir := filepath.Join(m.root, "jobs", id)
	if err := os.MkdirAll(jobDir, 0750); err != nil {
		return Job{}, fmt.Errorf("create job workspace: %w", err)
	}
	zipPath := filepath.Join(jobDir, "frontend.zip")
	if err := storage.SaveUpload(archive, zipPath); err != nil {
		_ = os.RemoveAll(jobDir)
		return Job{}, err
	}
	iconPath := ""
	if len(iconUploads) == 1 {
		iconPath = filepath.Join(jobDir, "icon-upload")
		if err := storage.SaveIconUpload(iconUploads[0], iconPath); err != nil {
			_ = os.RemoveAll(jobDir)
			return Job{}, err
		}
	}
	now := time.Now().UTC()
	job := Job{
		ID:        id,
		Status:    "queued",
		Stage:     "queued",
		Platform:  cfg.Platform,
		Framework: cfg.Frontend.Framework,
		AppName:   cfg.AppName,
		CreatedAt: now,
		UpdatedAt: now,
	}
	ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
	m.mu.Lock()
	m.jobs[id] = &record{job: job, config: cfg, cancel: cancel}
	m.mu.Unlock()
	go m.run(ctx, id, zipPath, iconPath, jobDir, cfg)
	return job, nil
}

func (m *Manager) Get(id string) (Job, error) {
	if !storage.ValidJobID(id) {
		return Job{}, ErrNotFound
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	item := m.jobs[id]
	if item == nil {
		return Job{}, ErrNotFound
	}
	return item.job, nil
}

func (m *Manager) StatusURL(id string) string {
	return m.publicURL + "/api/v1/builds/" + id
}

func (m *Manager) Cancel(id string) (Job, error) {
	if !storage.ValidJobID(id) {
		return Job{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.jobs[id]
	if item == nil {
		return Job{}, ErrNotFound
	}
	if item.job.Status == "completed" || item.job.Status == "failed" || item.job.Status == "cancelled" {
		return Job{}, ErrAlreadyFinished
	}
	item.cancel()
	item.job.Status = "cancelled"
	item.job.Stage = "cancelled"
	item.job.Message = "Build cancelled"
	item.job.UpdatedAt = time.Now().UTC()
	return item.job, nil
}

func (m *Manager) Artifact(id string) (string, string, error) {
	if !storage.ValidJobID(id) {
		return "", "", ErrNotFound
	}
	m.mu.RLock()
	item := m.jobs[id]
	if item == nil {
		m.mu.RUnlock()
		return "", "", ErrNotFound
	}
	job := item.job
	m.mu.RUnlock()
	if job.Status != "completed" || job.Artifact == "" {
		return "", "", ErrArtifactNotReady
	}
	extension := filepath.Ext(job.Artifact)
	path, err := storage.ArtifactPath(m.root, id, extension)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", ErrArtifactNotReady
	}
	return path, filepath.Base(path), nil
}

func (m *Manager) run(ctx context.Context, id, zipPath, iconPath, jobDir string, cfg config.Config) {
	defer os.RemoveAll(jobDir)
	select {
	case m.workers <- struct{}{}:
	case <-ctx.Done():
		m.fail(id, ctx.Err())
		return
	}
	defer func() { <-m.workers }()

	upload := filepath.Join(jobDir, "uploaded")
	if err := os.MkdirAll(upload, 0750); err != nil {
		m.fail(id, err)
		return
	}
	m.update(id, "extracting", 8, "Extracting frontend ZIP")
	if err := frontend.ExtractZip(ctx, zipPath, upload); err != nil {
		m.fail(id, err)
		return
	}
	root, err := frontend.LocateRoot(upload)
	if err != nil {
		m.fail(id, err)
		return
	}
	iconAssets := filepath.Join(jobDir, "generated-icons")
	if iconPath != "" {
		m.update(id, "generating-icons", 32, "Generating native application icons")
		if err := appicon.Generate(iconPath, iconAssets); err != nil {
			m.fail(id, err)
			return
		}
	}

	projectParent := filepath.Join(jobDir, "project")
	if err := os.MkdirAll(projectParent, 0750); err != nil {
		m.fail(id, err)
		return
	}
	m.update(id, "generating", 40, "Generating Wails 3 project")
	project, err := wailsbuilder.Generate(ctx, m.wailsBin, projectParent, cfg)
	if err != nil {
		m.fail(id, err)
		return
	}
	m.update(id, "configuring", 52, "Configuring native application resources")
	if iconPath != "" {
		if err := wailsbuilder.ApplyIcons(project, cfg.Platform, iconAssets); err != nil {
			m.fail(id, err)
			return
		}
	}
	if err := wailsbuilder.Configure(project, cfg); err != nil {
		m.fail(id, err)
		return
	}
	result, err := frontend.Build(ctx, root, cfg.Frontend, func(stage string, progress int, message string) {
		m.update(id, stage, 60+(progress-20), message)
	})
	if err != nil {
		m.fail(id, err)
		return
	}
	m.setFramework(id, result.Framework)
	m.update(id, "configuring", 74, "Integrating frontend assets and application metadata")
	if err := wailsbuilder.IntegrateFrontend(root, result.StaticDirectory, filepath.Join(project, "frontend"), result.Framework); err != nil {
		m.fail(id, err)
		return
	}
	m.update(id, "building", 82, "Compiling application with Wails 3")
	artifact, err := wailsbuilder.Compile(ctx, m.wailsBin, project, cfg, result.PackageManager, filepath.Join(result.StaticDirectory, "index.html"))
	if err != nil {
		m.fail(id, err)
		return
	}
	if err := ctx.Err(); err != nil {
		m.fail(id, err)
		return
	}
	final, err := storage.ArtifactPath(m.root, id, filepath.Ext(artifact))
	if err != nil {
		m.fail(id, err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(final), 0750); err != nil {
		m.fail(id, err)
		return
	}
	if err := storage.CopyFile(artifact, final); err != nil {
		m.fail(id, err)
		return
	}
	if info, err := os.Stat(final); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		m.fail(id, errors.New("compiled artifact is empty"))
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.jobs[id]
	if item == nil {
		_ = os.Remove(final)
		return
	}
	if item.job.Status == "cancelled" {
		_ = os.Remove(final)
		return
	}
	item.job.Status = "completed"
	item.job.Stage = "completed"
	item.job.Progress = 100
	item.job.Message = "Build completed"
	item.job.Artifact = filepath.Base(final)
	item.job.DownloadURL = m.publicURL + "/api/v1/builds/" + id + "/download"
	item.job.UpdatedAt = time.Now().UTC()
}

func (m *Manager) update(id, stage string, progress int, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if item := m.jobs[id]; item != nil && item.job.Status != "cancelled" {
		item.job.Status = "running"
		item.job.Stage = stage
		item.job.Progress = progress
		item.job.Message = message
		item.job.UpdatedAt = time.Now().UTC()
	}
}

func (m *Manager) setFramework(id, framework string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if item := m.jobs[id]; item != nil && item.job.Status != "cancelled" {
		item.job.Framework = framework
		item.job.UpdatedAt = time.Now().UTC()
	}
}

func (m *Manager) fail(id string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.jobs[id]
	if item == nil {
		return
	} else if item.job.Status == "cancelled" {
		return
	} else if errors.Is(err, context.Canceled) {
		item.job.Status = "cancelled"
		item.job.Stage = "cancelled"
		item.job.Message = "Build cancelled"
	} else {
		item.job.Status = "failed"
		item.job.Stage = "failed"
		item.job.Error = err.Error()
	}
	item.job.UpdatedAt = time.Now().UTC()
}

func newID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}
