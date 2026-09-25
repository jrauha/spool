package feed

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/spool-reader/spool/internal/core"
)

type refreshHandlerFunc func(context.Context, RefreshArgs) error

func (f refreshHandlerFunc) RefreshJob(ctx context.Context, args RefreshArgs) error {
	return f(ctx, args)
}

type permanentTestError struct{}

func (permanentTestError) Error() string   { return "permanent" }
func (permanentTestError) Permanent() bool { return true }

func TestRefreshWorkerCompletesSuccess(t *testing.T) {
	worker := NewRefreshWorker(refreshHandlerFunc(func(context.Context, RefreshArgs) error { return nil }), nil)
	if err := worker.Work(context.Background(), &river.Job[RefreshArgs]{Args: RefreshArgs{FeedID: "feed-1"}}); err != nil {
		t.Fatalf("Work returned error: %v", err)
	}
	if got := worker.refreshSucceeded.Load(); got != 1 {
		t.Fatalf("success count = %d, want 1", got)
	}
}

func TestRefreshWorkerReturnsTransientErrorForRiverRetry(t *testing.T) {
	refreshErr := errors.New("temporary")
	worker := NewRefreshWorker(refreshHandlerFunc(func(context.Context, RefreshArgs) error { return refreshErr }), nil)
	err := worker.Work(context.Background(), &river.Job[RefreshArgs]{Args: RefreshArgs{FeedID: "feed-1"}})
	if !errors.Is(err, refreshErr) {
		t.Fatalf("Work error = %v, want %v", err, refreshErr)
	}
	if got := worker.refreshRetried.Load(); got != 1 {
		t.Fatalf("retry count = %d, want 1", got)
	}
}

func TestRefreshWorkerCancelsPermanentError(t *testing.T) {
	worker := NewRefreshWorker(refreshHandlerFunc(func(context.Context, RefreshArgs) error { return permanentTestError{} }), nil)
	err := worker.Work(context.Background(), &river.Job[RefreshArgs]{Args: RefreshArgs{FeedID: "feed-1"}})
	var cancelled *river.JobCancelError
	if !errors.As(err, &cancelled) {
		t.Fatalf("Work error = %v, want River cancellation", err)
	}
	if got := worker.refreshPermanent.Load(); got != 1 {
		t.Fatalf("permanent count = %d, want 1", got)
	}
}

func TestRefreshWorkerPrometheusMetrics(t *testing.T) {
	worker := NewRefreshWorker(refreshHandlerFunc(func(context.Context, RefreshArgs) error { return nil }), nil)
	worker.refreshSucceeded.Add(1)
	worker.refreshFailed.Add(2)
	metrics := worker.PrometheusMetrics()
	for _, want := range []string{"spool_refresh_succeeded_total 1", "spool_refresh_failed_total 2"} {
		if !strings.Contains(metrics, want) {
			t.Fatalf("metrics = %q, want %q", metrics, want)
		}
	}
}

type dueFeedStore struct {
	feeds    []core.Feed
	interval time.Duration
	limit    int
}

func (s *dueFeedStore) ListFeedsDueRefresh(_ context.Context, interval time.Duration, limit int) ([]core.Feed, error) {
	s.interval = interval
	s.limit = limit
	return s.feeds, nil
}

type refreshJobRecorder struct {
	args []RefreshArgs
}

func (*refreshJobRecorder) InsertRefresh(context.Context, RefreshArgs) error { return nil }
func (r *refreshJobRecorder) InsertRefreshBatch(_ context.Context, args []RefreshArgs) error {
	r.args = append(r.args, args...)
	return nil
}

func TestRefreshScheduleWorkerBatchEnqueuesDueFeeds(t *testing.T) {
	refreshedAt := time.Now().UTC().Add(-time.Hour)
	store := &dueFeedStore{feeds: []core.Feed{{ID: "feed-1", URL: "https://example.com/feed", RefreshedAt: &refreshedAt}}}
	jobs := &refreshJobRecorder{}
	worker := NewRefreshScheduleWorker(store, jobs)
	if err := worker.Work(context.Background(), &river.Job[ScheduleRefreshArgs]{}); err != nil {
		t.Fatalf("Work returned error: %v", err)
	}
	if store.interval != defaultRefreshInterval || store.limit != scheduleRefreshBatch {
		t.Fatalf("query = (%s, %d)", store.interval, store.limit)
	}
	want := refreshArgs(store.feeds[0].ID, store.feeds[0].URL, &refreshedAt)
	if len(jobs.args) != 1 || jobs.args[0] != want {
		t.Fatalf("enqueued args = %#v, want %#v", jobs.args, []RefreshArgs{want})
	}
}
