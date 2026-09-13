package feed

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/spool-reader/spool/internal/core"
)

func TestWorkerSchedulesDueRefreshes(t *testing.T) {
	store := &workerStore{}
	worker := NewWorker(store, refreshFunc(func(ctx context.Context, id string) error { return nil }), nil)

	if err := worker.Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule returned error: %v", err)
	}
	if !store.scheduled {
		t.Fatal("due refreshes were not scheduled")
	}
}

func TestWorkerCompletesRefresh(t *testing.T) {
	store := &workerStore{job: core.RefreshJob{FeedID: "feed-1", LeaseToken: "lease"}}
	worker := NewWorker(store, refreshFunc(func(ctx context.Context, id string) error { return nil }), nil)

	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	if !store.completed {
		t.Fatal("job was not completed")
	}
}

func TestWorkerRetriesFailedRefresh(t *testing.T) {
	store := &workerStore{job: core.RefreshJob{FeedID: "feed-1", LeaseToken: "lease"}}
	refreshErr := errors.New("fetch failed")
	worker := NewWorker(store, refreshFunc(func(ctx context.Context, id string) error { return refreshErr }), nil)

	err := worker.RunOnce(context.Background())
	if !errors.Is(err, refreshErr) {
		t.Fatalf("RunOnce error = %v, want %v", err, refreshErr)
	}
	if !store.retried {
		t.Fatal("job was not retried")
	}
}

type refreshFunc func(ctx context.Context, id string) error

func (f refreshFunc) Refresh(ctx context.Context, id string) error {
	return f(ctx, id)
}

type workerStore struct {
	job       core.RefreshJob
	completed bool
	retried   bool
	scheduled bool
}

func (s *workerStore) EnqueueDueRefresh(ctx context.Context, interval time.Duration) (int, error) {
	s.scheduled = true
	return 0, nil
}

func (s *workerStore) ClaimRefresh(ctx context.Context, lease time.Duration) (core.RefreshJob, error) {
	if s.job.FeedID == "" {
		return core.RefreshJob{}, core.ErrNoRefreshJob
	}
	job := s.job
	s.job = core.RefreshJob{}
	return job, nil
}

func (s *workerStore) CompleteRefresh(ctx context.Context, feedID, leaseToken string) (bool, error) {
	s.completed = true
	return true, nil
}

func (s *workerStore) RetryRefresh(ctx context.Context, feedID, leaseToken string, availableAt time.Time) (bool, error) {
	s.retried = true
	return true, nil
}
