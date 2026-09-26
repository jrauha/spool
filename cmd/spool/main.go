package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/riverqueue/river"
	"github.com/spool-reader/spool/internal/asset"
	"github.com/spool-reader/spool/internal/auth"
	"github.com/spool-reader/spool/internal/config"
	"github.com/spool-reader/spool/internal/core"
	"github.com/spool-reader/spool/internal/db"
	"github.com/spool-reader/spool/internal/feed"
	"github.com/spool-reader/spool/internal/mailer"
	"github.com/spool-reader/spool/internal/server"
	"github.com/spool-reader/spool/internal/thumbnail"
)

const (
	defaultThumbnailWorkers = 2
	assetDirectoryName      = "assets"
	serverCommand           = "server"
	workerCommand           = "worker"
	schedulerCommand        = "scheduler"
	migrateCommand          = "migrate"
	usageMessage            = "usage: spool [server|worker|scheduler|migrate]"
)

func main() {
	_ = godotenv.Load()

	cfg := config.FromEnv()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	command, err := parseCommand(os.Args[1:])
	if err != nil {
		log.Error(err.Error())
		os.Exit(2)
	}

	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		log.Error("database open failed", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	if command == migrateCommand {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := db.Migrate(ctx, database); err != nil {
			log.Error("database migration failed", "error", err)
			os.Exit(1)
		}
		log.Info("database migrations completed")
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var runErr error
	switch command {
	case serverCommand:
		runErr = runServer(ctx, cfg, log, database)
	case workerCommand:
		runErr = runWorker(ctx, cfg, log, database)
	case schedulerCommand:
		runErr = runScheduler(ctx, log, database)
	}
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		log.Error("spool stopped", "role", command, "error", runErr)
		os.Exit(1)
	}
}

func parseCommand(args []string) (string, error) {
	if len(args) == 0 {
		return serverCommand, nil
	}
	if len(args) == 1 && (args[0] == serverCommand || args[0] == workerCommand || args[0] == schedulerCommand || args[0] == migrateCommand) {
		return args[0], nil
	}
	return "", errors.New(usageMessage)
}

func runServer(ctx context.Context, cfg config.Config, log *slog.Logger, database *sql.DB) error {
	store := auth.NewPostgresStore(database)
	coreStore := core.NewPostgresStore(database)
	authSvc := auth.NewService(store)
	if cfg.SMTPAddr != "" {
		resetSender, err := mailer.NewSMTPPasswordResetSender(
			cfg.SMTPAddr, cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPFrom, cfg.PublicURL,
		)
		if err != nil {
			return err
		}
		authSvc = auth.NewServiceWithPasswordReset(store, resetSender)
	}
	setupRequired, err := authSvc.SetupRequired(ctx)
	if err != nil {
		return err
	}
	if setupRequired && strings.TrimSpace(cfg.SetupToken) == "" {
		return errors.New("SPOOL_SETUP_TOKEN is required before initial setup")
	}

	riverClient, err := newRiverInsertClient(database, log)
	if err != nil {
		return err
	}
	assetSvc := asset.NewService(coreStore, filepath.Join(cfg.SpoolHome, assetDirectoryName))
	thumbnailJobs := &riverThumbnailJobs{client: riverClient}
	feedSvc := feed.NewServiceWithItemUpsertHook(coreStore, &riverFeedJobs{client: riverClient}, thumbnailItemUpsertHook(thumbnailJobs), nil)
	srv := server.NewWithAssets(cfg, log, authSvc, feedSvc, assetSvc, database.PingContext, nil)
	serverErr := make(chan error, 1)
	go func() {
		log.Info("starting spool server", "addr", cfg.Addr)
		serverErr <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func runWorker(ctx context.Context, cfg config.Config, log *slog.Logger, database *sql.DB) error {
	store := core.NewPostgresStore(database)
	assetSvc := asset.NewService(store, filepath.Join(cfg.SpoolHome, assetDirectoryName))
	client, err := newRiverWorkerClient(database, store, assetSvc, log, map[string]river.QueueConfig{
		river.QueueDefault: {MaxWorkers: defaultFeedWorkers},
		thumbnail.JobQueue: {MaxWorkers: defaultThumbnailWorkers},
	})
	if err != nil {
		return err
	}
	log.Info("starting spool River worker", "queue", river.QueueDefault)
	return runRiverClient(ctx, client)
}

func runScheduler(ctx context.Context, log *slog.Logger, database *sql.DB) error {
	client, err := newRiverSchedulerClient(database, core.NewPostgresStore(database), log)
	if err != nil {
		return err
	}
	log.Info("starting spool River scheduler", "queue", feed.RefreshScheduleQueue)
	return runRiverClient(ctx, client)
}

func runRiverClient(ctx context.Context, client *river.Client[*sql.Tx]) error {
	if err := client.Start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.Stop(shutdownCtx); err != nil {
		return err
	}
	return ctx.Err()
}
