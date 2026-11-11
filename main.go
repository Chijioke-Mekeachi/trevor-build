package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"trevorbuild/internal/api"
	"trevorbuild/internal/builds"
)

func main() {
	root := env("TREVORBUILD_DATA", "./data")
	if err := os.MkdirAll(filepath.Join(root, "jobs"), 0750); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "artifacts"), 0750); err != nil {
		log.Fatal(err)
	}
	workers, err := strconv.Atoi(env("BUILD_WORKERS", "1"))
	if err != nil || workers < 1 {
		log.Fatal("BUILD_WORKERS must be a positive integer")
	}
	manager := builds.NewManager(
		root,
		strings.TrimRight(env("PUBLIC_BASE_URL", "http://localhost:8080"), "/"),
		env("WAILS_BIN", "wails3"),
		workers,
	)

	addr := env("LISTEN_ADDR", ":8080")
	server := &http.Server{
		Addr:              addr,
		Handler:           api.NewHandler(manager),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("TrevorBuild API listening on %s", addr)
	log.Fatal(server.ListenAndServe())
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
