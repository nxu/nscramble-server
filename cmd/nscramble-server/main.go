// nscramble-server syncs solves between the nscramble apps.
//
// Configuration (environment):
//
//	NSCRAMBLE_API_KEY  required, at least 16 characters; clients send it as "Authorization: Bearer <key>"
//	NSCRAMBLE_DB       SQLite database path (default data/nscramble.sqlite)
//	NSCRAMBLE_ADDR     listen address (default :8080)
//	NSCRAMBLE_TZ       IANA time zone that decides "today" for /stats, e.g. Europe/Budapest (default UTC)
//
// "nscramble-server healthcheck" exits 0 if the server on NSCRAMBLE_ADDR answers /health (for Docker).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	_ "time/tzdata" // the distroless image has no zoneinfo

	"nscramble-server/internal/server"
	"nscramble-server/internal/store"
)

const minAPIKeyLength = 16

func main() {
	addr := getenv("NSCRAMBLE_ADDR", ":8080")
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck(addr))
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log, addr); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, addr string) error {
	apiKey := os.Getenv("NSCRAMBLE_API_KEY")
	if len(apiKey) < minAPIKeyLength {
		return fmt.Errorf("NSCRAMBLE_API_KEY must be set to at least %d characters", minAPIKeyLength)
	}
	location, err := time.LoadLocation(getenv("NSCRAMBLE_TZ", "UTC"))
	if err != nil {
		return fmt.Errorf("NSCRAMBLE_TZ: %w", err)
	}
	dbPath := getenv("NSCRAMBLE_DB", "data/nscramble.sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer st.Close()

	srv := &http.Server{
		Addr:              addr,
		Handler:           server.New(st, apiKey, location, log),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", addr, "db", dbPath, "tz", location.String())
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func healthcheck(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 1
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/health")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
