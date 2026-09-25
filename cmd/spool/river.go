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
	"github.com/spool-reader/spool/internal/core"
	"github.com/spool-reader/spool/internal/feed"
)

const defaultFeedWorkers = 10

type riverFeedJobs struct {
	client *river.Client[*sql.Tx]
}

func newRiverInsertClient(database *sql.DB, log *slog.Logger) (*river.Client[*sql.Tx], error) {
	client, err := river.NewClient(riverdatabasesql.New(database), &river.Config{Logger: log})
	if err != nil {
		return nil, fmt.Errorf("create River client: %w", err)
	}
	return client, nil
}

func newRiverWorkerClient(database *sql.DB, store *core.PostgresStore, log *slog.Logger, queues map[string]river.QueueConfig) (*river.Client[*sql.Tx], error) {
	jobs := &riverFeedJobs{}
	feedService := feed.NewService(store, nil)
	workers := river.NewWorkers()
	river.AddWorker[feed.RefreshArgs](workers, feed.NewRefreshWorker(feedService, log))
	river.AddWorker[feed.ScheduleRefreshArgs](workers, feed.NewRefreshScheduleWorker(store, jobs))
	client, err := river.NewClient(riverdatabasesql.New(database), &river.Config{
		Logger:       log,
		PeriodicJobs: feed.PeriodicJobs(),
		Queues:       queues,
		Workers:      workers,
	})
	if err != nil {
		return nil, fmt.Errorf("create River worker client: %w", err)
	}
	jobs.client = client
	return client, nil
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
