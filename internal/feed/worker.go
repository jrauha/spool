package feed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/spool-reader/spool/internal/core"
)

const (
	defaultJobTimeout       = 30 * time.Second
	defaultJobLease         = 2 * time.Minute
	defaultRetryDelay       = time.Minute
	defaultMaxRetryDelay    = time.Hour
	maxBackoffExponent      = 6
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
	jobs             JobStore
	feed             Refresher
	log              *slog.Logger
	refreshSucceeded atomic.Uint64
	refreshFailed    atomic.Uint64
	refreshPermanent atomic.Uint64
	refreshRetried   atomic.Uint64
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
			_ = w.RunOnce(ctx)
		}
	}
}

func (w *Worker) Schedule(ctx context.Context) error {
	count, err := w.jobs.EnqueueDueRefresh(ctx, defaultRefreshInterval)
	if err == nil && count != 0 {
		w.log.Debug("scheduled feed refreshes", "count", count)
	}
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
		w.refreshFailed.Add(1)
		if isPermanentRefreshError(err) {
			w.refreshPermanent.Add(1)
			if completeErr := w.complete(ctx, job); completeErr != nil {
				return completeErr
			}
			w.log.Warn("feed refresh failed permanently", "feed_id", job.FeedID, "attempt", job.Attempts, "error", err)
			return err
		}
		availableAt, retryErr := w.retry(ctx, job)
		if retryErr != nil {
			return retryErr
		}
		w.refreshRetried.Add(1)
		w.log.Warn("feed refresh failed; retry scheduled", "feed_id", job.FeedID, "attempt", job.Attempts, "retry_at", availableAt, "error", err)
		return err
	}

	if err := w.complete(ctx, job); err != nil {
		return err
	}
	w.refreshSucceeded.Add(1)
	w.log.Debug("feed refreshed", "feed_id", job.FeedID)
	return nil
}

func (w *Worker) complete(ctx context.Context, job core.RefreshJob) error {
	completed, err := w.jobs.CompleteRefresh(ctx, job.FeedID, job.LeaseToken)
	if err != nil {
		return err
	}
	if !completed {
		return ErrRefreshLeaseLost
	}
	return nil
}

func (w *Worker) retry(ctx context.Context, job core.RefreshJob) (time.Time, error) {
	availableAt := time.Now().UTC().Add(retryDelay(job.Attempts))
	retried, err := w.jobs.RetryRefresh(ctx, job.FeedID, job.LeaseToken, availableAt)
	if err != nil {
		return time.Time{}, err
	}
	if !retried {
		return time.Time{}, ErrRefreshLeaseLost
	}
	return availableAt, nil
}

type permanentError interface {
	Permanent() bool
}

func isPermanentRefreshError(err error) bool {
	var permanent permanentError
	return errors.As(err, &permanent) && permanent.Permanent()
}

func retryDelay(attempts int) time.Duration {
	exponent := attempts - 1
	if exponent < 0 {
		exponent = 0
	}
	if exponent > maxBackoffExponent {
		exponent = maxBackoffExponent
	}
	delay := defaultRetryDelay * time.Duration(1<<exponent)
	if delay > defaultMaxRetryDelay {
		return defaultMaxRetryDelay
	}
	return delay
}

func (w *Worker) PrometheusMetrics() string {
	var metrics strings.Builder
	writeMetric := func(name string, value uint64) {
		fmt.Fprintf(&metrics, "# TYPE %s counter\n%s %d\n", name, name, value)
	}
	writeMetric("spool_refresh_succeeded_total", w.refreshSucceeded.Load())
	writeMetric("spool_refresh_failed_total", w.refreshFailed.Load())
	writeMetric("spool_refresh_permanent_total", w.refreshPermanent.Load())
	writeMetric("spool_refresh_retried_total", w.refreshRetried.Load())
	return metrics.String()
}
