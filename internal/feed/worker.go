package feed

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/spool-reader/spool/internal/core"
)

const (
	defaultJobTimeout       = 30 * time.Second
	defaultJobLease         = 2 * time.Minute
	defaultRetryDelay       = time.Minute
	defaultRefreshInterval  = 15 * time.Minute
	defaultScheduleInterval = time.Minute
	defaultWorkerPoll       = time.Second
)

var ErrRefreshLeaseLost = errors.New("refresh job lease lost")

type JobStore interface {
	EnqueueDueRefresh(ctx context.Context, interval time.Duration) (int, error)
	ClaimRefresh(ctx context.Context, lease time.Duration) (core.RefreshJob, error)
	CompleteRefresh(ctx context.Context, feedID, leaseToken string) (bool, error)
	RetryRefresh(ctx context.Context, feedID, leaseToken string, availableAt time.Time) (bool, error)
}

type Refresher interface {
	Refresh(ctx context.Context, id string) error
}

type Worker struct {
	jobs JobStore
	feed Refresher
	log  *slog.Logger
}

func NewWorker(jobs JobStore, feed Refresher, log *slog.Logger) *Worker {
	if log == nil {
		log = slog.Default()
	}
	return &Worker{jobs: jobs, feed: feed, log: log}
}

func (w *Worker) Run(ctx context.Context) error {
	if err := w.Schedule(ctx); err != nil {
		w.log.Error("schedule refreshes", "error", err)
	}

	schedule := time.NewTicker(defaultScheduleInterval)
	defer schedule.Stop()
	poll := time.NewTicker(defaultWorkerPoll)
	defer poll.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-schedule.C:
			if err := w.Schedule(ctx); err != nil {
				w.log.Error("schedule refreshes", "error", err)
			}
		case <-poll.C:
			if err := w.RunOnce(ctx); err != nil {
				w.log.Error("refresh feed", "error", err)
			}
		}
	}
}

func (w *Worker) Schedule(ctx context.Context) error {
	_, err := w.jobs.EnqueueDueRefresh(ctx, defaultRefreshInterval)
	return err
}

func (w *Worker) RunOnce(ctx context.Context) error {
	job, err := w.jobs.ClaimRefresh(ctx, defaultJobLease)
	if errors.Is(err, core.ErrNoRefreshJob) {
		return nil
	}
	if err != nil {
		return err
	}

	refreshCtx, cancel := context.WithTimeout(ctx, defaultJobTimeout)
	err = w.feed.Refresh(refreshCtx, job.FeedID)
	cancel()
	if err != nil {
		if retryErr := w.retry(ctx, job); retryErr != nil {
			return retryErr
		}
		return err
	}

	completed, err := w.jobs.CompleteRefresh(ctx, job.FeedID, job.LeaseToken)
	if err != nil {
		return err
	}
	if !completed {
		return ErrRefreshLeaseLost
	}
	return nil
}

func (w *Worker) retry(ctx context.Context, job core.RefreshJob) error {
	retried, err := w.jobs.RetryRefresh(ctx, job.FeedID, job.LeaseToken, time.Now().UTC().Add(defaultRetryDelay))
	if err != nil {
		return err
	}
	if !retried {
		return ErrRefreshLeaseLost
	}
	return nil
}
