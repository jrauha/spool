package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/spool-reader/spool/internal/auth"
	"github.com/spool-reader/spool/internal/config"
	"github.com/spool-reader/spool/internal/core"
	"github.com/spool-reader/spool/internal/db"
	"github.com/spool-reader/spool/internal/feed"
	"github.com/spool-reader/spool/internal/server"
)

func main() {
	_ = godotenv.Load()

	cfg := config.FromEnv()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		log.Error("database open failed", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	migrateCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.Migrate(migrateCtx, database); err != nil {
		log.Error("database migration failed", "error", err)
		os.Exit(1)
	}

	store := auth.NewPostgresStore(database)
	coreStore := core.NewPostgresStore(database)
	authSvc := auth.NewService(store)
	feedSvc := feed.NewService(coreStore, nil)
	worker := feed.NewWorker(coreStore, feedSvc, log)
	srv := server.New(cfg, log, authSvc, feedSvc)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		if err := worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("refresh worker failed", "error", err)
		}
	}()

	go func() {
		log.Info("starting spool", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown failed", "error", err)
		os.Exit(1)
	}
	select {
	case <-workerDone:
	case <-shutdownCtx.Done():
		log.Error("refresh worker shutdown timed out")
	}
}
