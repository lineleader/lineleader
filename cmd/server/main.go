package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lineleader/lineleader/internal/dvc"
	"github.com/lineleader/lineleader/internal/ledger"
	"github.com/lineleader/lineleader/internal/web"
)

// Timeouts sized for an htmx app: plain request/response cycles, no
// streaming or long-poll connections to keep alive.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 10 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownTimeout   = 10 * time.Second
)

func main() {
	dataDir := flag.String("data-dir", "data/point-charts", "directory with JSON chart files")
	configFile := flag.String("config", dvc.DefaultConfigPath(), "app config file (JSON)")
	ledgerDSN := flag.String("ledger-dsn", os.Getenv("LEDGER_DSN"), "points ledger Postgres DSN (or set LEDGER_DSN)")
	addr := flag.String("addr", ":8080", "listen address")
	authSecretFile := flag.String("auth-secret-file", "", "path to a file containing the shared auth secret (alternative to AUTH_SECRET; for mounted Docker secrets)")
	insecureCookies := flag.Bool("insecure-cookies", false, "omit the Secure attribute on the session cookie (plain-http local dev only; never use in a real deployment)")
	flag.Parse()

	if *ledgerDSN == "" {
		fmt.Fprintln(os.Stderr, "ledger: no database DSN provided (use --ledger-dsn or set LEDGER_DSN)")
		os.Exit(1)
	}

	authSecret, err := resolveAuthSecret(*authSecretFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "auth: %v\n", err)
		os.Exit(1)
	}

	charts, err := dvc.LoadAll(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading charts: %v\n", err)
		os.Exit(1)
	}
	if len(charts) == 0 {
		fmt.Fprintf(os.Stderr, "no charts found in %s — run 'dvc import' first\n", *dataDir)
		os.Exit(1)
	}

	cfg, err := dvc.LoadConfig(*configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: loading config %s: %v\n", *configFile, err)
	}

	ledgerStore, err := ledger.Open(*ledgerDSN)
	if err != nil {
		// Don't log *ledgerDSN — it can embed a password.
		fmt.Fprintf(os.Stderr, "opening ledger: %v\n", err)
		os.Exit(1)
	}

	srv := web.NewServer(web.Options{
		Charts:        charts,
		Config:        cfg,
		ConfigPath:    *configFile,
		Ledger:        ledgerStore,
		AuthSecret:    authSecret,
		SecureCookies: !*insecureCookies,
	})

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("lineleader web listening on %s", *addr)
		serverErr <- httpServer.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	case sig := <-sigCh:
		log.Printf("received %s, shutting down", sig)
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(ctx); err != nil {
			log.Printf("graceful shutdown: %v", err)
		}
	}

	if err := ledgerStore.Close(); err != nil {
		log.Printf("closing ledger: %v", err)
	}
	log.Printf("clean shutdown complete")
}

// resolveAuthSecret reads the single shared auth secret from a mounted
// file (Docker-friendly) or the AUTH_SECRET env var, in that order. Per
// docs/pitches/hosted-lineleader.md there is exactly one secret and no
// users table — the server refuses to start without one rather than
// silently serving unauthenticated.
func resolveAuthSecret(secretFile string) (string, error) {
	if secretFile != "" {
		b, err := os.ReadFile(secretFile)
		if err != nil {
			return "", fmt.Errorf("reading --auth-secret-file %s: %w", secretFile, err)
		}
		secret := strings.TrimSpace(string(b))
		if secret == "" {
			return "", fmt.Errorf("--auth-secret-file %s is empty", secretFile)
		}
		return secret, nil
	}
	if secret := strings.TrimSpace(os.Getenv("AUTH_SECRET")); secret != "" {
		return secret, nil
	}
	return "", errors.New("no auth secret provided (set AUTH_SECRET or --auth-secret-file)")
}
