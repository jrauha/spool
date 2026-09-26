package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/spool-reader/spool/internal/asset"
	"github.com/spool-reader/spool/internal/core"
	"github.com/spool-reader/spool/internal/feed"
	"github.com/spool-reader/spool/internal/thumbnail"
)

const defaultFeedWorkers = 10

type riverFeedJobs struct {
	client *river.Client[*sql.Tx]
}

type riverThumbnailJobs struct {
	client *river.Client[*sql.Tx]
}

func newRiverInsertClient(database *sql.DB, log *slog.Logger) (*river.Client[*sql.Tx], error) {
	client, err := river.NewClient(riverdatabasesql.New(database), &river.Config{Logger: log})
	if err != nil {
		return nil, fmt.Errorf("create River client: %w", err)
	}
	return client, nil
}

func newRiverWorkerClient(database *sql.DB, store *core.PostgresStore, assets *asset.Service, log *slog.Logger, queues map[string]river.QueueConfig) (*river.Client[*sql.Tx], error) {
	thumbnailJobs := &riverThumbnailJobs{}
	feedService := feed.NewServiceWithItemUpsertHook(store, nil, thumbnailItemUpsertHook(thumbnailJobs), nil)
	thumbnailService := thumbnail.NewService(store, assets, nil)
	workers := river.NewWorkers()
	river.AddWorker[feed.RefreshArgs](workers, feed.NewRefreshWorker(feedService, log))
	river.AddWorker[thumbnail.JobArgs](workers, thumbnail.NewWorker(thumbnailService))
	river.AddWorker[thumbnail.CleanupArgs](workers, thumbnail.NewCleanupWorker(assets))
	client, err := newRiverClient(database, log, queues, workers, thumbnail.PeriodicJobs())
	if err != nil {
		return nil, err
	}
	thumbnailJobs.client = client
	return client, nil
}

func newRiverSchedulerClient(database *sql.DB, store *core.PostgresStore, log *slog.Logger) (*river.Client[*sql.Tx], error) {
	jobs := &riverFeedJobs{}
	workers := river.NewWorkers()
	river.AddWorker[feed.ScheduleRefreshArgs](workers, feed.NewRefreshScheduleWorker(store, jobs))
	client, err := newRiverClient(database, log, map[string]river.QueueConfig{
		feed.RefreshScheduleQueue: {MaxWorkers: 1},
	}, workers, feed.PeriodicJobs())
	if err != nil {
		return nil, err
	}
	jobs.client = client
	return client, nil
}

func newRiverClient(database *sql.DB, log *slog.Logger, queues map[string]river.QueueConfig, workers *river.Workers, periodicJobs []*river.PeriodicJob) (*river.Client[*sql.Tx], error) {
	client, err := river.NewClient(riverdatabasesql.New(database), &river.Config{
		Logger:       log,
		PeriodicJobs: periodicJobs,
		Queues:       queues,
		Workers:      workers,
	})
	if err != nil {
		return nil, fmt.Errorf("create River client: %w", err)
	}
	return client, nil
}

func thumbnailItemUpsertHook(jobs *riverThumbnailJobs) core.ItemUpsertHook {
	if jobs == nil {
		return nil
	}
	return func(ctx context.Context, tx *sql.Tx, item core.Item) error {
		if err := jobs.InsertThumbnailTx(ctx, tx, thumbnail.JobArgs{
			ItemID:   item.ID,
			PageURL:  item.URL,
			ImageURL: item.ImageURL,
		}); err != nil {
			return fmt.Errorf("enqueue item thumbnail: %w", err)
		}
		return nil
	}
}

func (j *riverThumbnailJobs) InsertThumbnailTx(ctx context.Context, tx *sql.Tx, args thumbnail.JobArgs) error {
	if j.client == nil {
		return errors.New("River client not initialized")
	}
	if _, err := j.client.InsertTx(ctx, tx, args, nil); err != nil {
		return fmt.Errorf("insert thumbnail job: %w", err)
	}
	return nil
}

func (j *riverFeedJobs) InsertRefresh(ctx context.Context, args feed.RefreshArgs) error {
	if j.client == nil {
		return errors.New("River client not initialized")
	}
	if _, err := j.client.Insert(ctx, args, nil); err != nil {
		return fmt.Errorf("insert feed refresh job: %w", err)
	}
	return nil
}

func (j *riverFeedJobs) InsertRefreshBatch(ctx context.Context, args []feed.RefreshArgs) error {
	if len(args) == 0 {
		return nil
	}
	if j.client == nil {
		return errors.New("River client not initialized")
	}
	params := make([]river.InsertManyParams, 0, len(args))
	for _, arg := range args {
		params = append(params, river.InsertManyParams{Args: arg})
	}
	if _, err := j.client.InsertManyFast(ctx, params); err == nil {
		return nil
	} else {
		var postgresErr *pgconn.PgError
		if !errors.As(err, &postgresErr) || postgresErr.Code != "23505" {
			return fmt.Errorf("insert feed refresh batch: %w", err)
		}
	}

	// River's fast batch path reports unique conflicts as errors; individual
	// inserts preserve River's normal skip-on-duplicate behavior.
	for _, arg := range args {
		if err := j.InsertRefresh(ctx, arg); err != nil {
			return err
		}
	}
	return nil
}
