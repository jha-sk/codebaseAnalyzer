// Command dashboard serves the codebase-analyser web dashboard: an HTTP API
// that ingests CLI run results plus the embedded single-page frontend.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"codebase-analyser/internal/dashboard/api"
	"codebase-analyser/internal/dashboard/store"
	"codebase-analyser/internal/dashboard/web"
)

func main() {
	// Config is env-only: this runs from docker-compose, where env is the
	// native way to pass secrets, and there is nothing here a flag would
	// serve better.
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required (e.g. postgres://user:pass@db:5432/analyser?sslmode=disable)")
	}
	adminToken := os.Getenv("DASHBOARD_ADMIN_TOKEN")
	if adminToken == "" {
		log.Fatal("DASHBOARD_ADMIN_TOKEN is required; it gates the UI and all repo management")
	}
	addr := os.Getenv("DASHBOARD_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer st.Close()

	srv := &http.Server{
		Addr:              addr,
		Handler:           api.New(st, adminToken, web.Assets),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("dashboard listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
