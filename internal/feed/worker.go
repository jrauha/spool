package feed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/riverqueue/river"
	"github.com/spool-reader/spool/internal/core"
)

type RefreshJobInserter interface {
	InsertRefresh(ctx context.Context, args RefreshArgs) error
	InsertRefreshBatch(ctx context.Context, args []RefreshArgs) error
}

type RefreshJobHandler interface {
	RefreshJob(ctx context.Context, args RefreshArgs) error
}

type DueFeedStore interface {
	ListFeedsDueRefresh(ctx context.Context, interval time.Duration, limit int) ([]core.Feed, error)
}

type RefreshWorker struct {
	river.WorkerDefaults[RefreshArgs]
	refresher        RefreshJobHandler
	log              *slog.Logger
	refreshSucceeded atomic.Uint64
	refreshFailed    atomic.Uint64
	refreshPermanent atomic.Uint64
	refreshRetried   atomic.Uint64
}

func NewRefreshWorker(refresher RefreshJobHandler, log *slog.Logger) *RefreshWorker {
	if log == nil {
		log = slog.Default()
	}
	return &RefreshWorker{refresher: refresher, log: log}
}

func (w *RefreshWorker) Timeout(*river.Job[RefreshArgs]) time.Duration {
	return defaultJobTimeout
}

func (w *RefreshWorker) Work(ctx context.Context, current *river.Job[RefreshArgs]) error {
	err := w.refresher.RefreshJob(ctx, current.Args)
	if err != nil {
		w.refreshFailed.Add(1)
		if isPermanentRefreshError(err) || errors.Is(err, core.ErrFeedNotFound) {
			w.refreshPermanent.Add(1)
			w.log.WarnContext(ctx, "feed refresh failed permanently", "feed_id", current.Args.FeedID, "error", err)
			return river.JobCancel(err)
		}
		w.refreshRetried.Add(1)
		return err
	}
	w.refreshSucceeded.Add(1)
	return nil
}

func (w *RefreshWorker) PrometheusMetrics() string {
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

type RefreshScheduleWorker struct {
	river.WorkerDefaults[ScheduleRefreshArgs]
	feeds DueFeedStore
	jobs  RefreshJobInserter
}

func NewRefreshScheduleWorker(feeds DueFeedStore, jobs RefreshJobInserter) *RefreshScheduleWorker {
	return &RefreshScheduleWorker{feeds: feeds, jobs: jobs}
}

func (w *RefreshScheduleWorker) Work(ctx context.Context, _ *river.Job[ScheduleRefreshArgs]) error {
	feeds, err := w.feeds.ListFeedsDueRefresh(ctx, defaultRefreshInterval, scheduleRefreshBatch)
	if err != nil {
		return err
	}
	args := make([]RefreshArgs, 0, len(feeds))
	for _, feed := range feeds {
		args = append(args, refreshArgs(feed.ID, feed.URL, feed.RefreshedAt))
	}
	if err := w.jobs.InsertRefreshBatch(ctx, args); err != nil {
		return fmt.Errorf("insert scheduled feed refreshes: %w", err)
	}
	return nil
}

func isPermanentRefreshError(err error) bool {
	var permanent interface{ Permanent() bool }
	return errors.As(err, &permanent) && permanent.Permanent()
}
