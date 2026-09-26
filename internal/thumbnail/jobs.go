package thumbnail

import (
	"context"
	"errors"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

const (
	JobQueue           = "spool-thumbnails"
	jobKind            = "item.thumbnail"
	cleanupJobKind     = "item.thumbnail.cleanup"
	maxJobAttempts     = 5
	maxCleanupAttempts = 3
	jobTimeout         = 90 * time.Second
	cleanupJobTimeout  = 10 * time.Minute
	cleanupInterval    = 24 * time.Hour
)

type JobArgs struct {
	ItemID   string `json:"item_id"`
	PageURL  string `json:"page_url"`
	ImageURL string `json:"image_url,omitempty"`
}

func (JobArgs) Kind() string { return jobKind }

func (JobArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: maxJobAttempts,
		Queue:       JobQueue,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable,
				rivertype.JobStatePending,
				rivertype.JobStateRunning,
				rivertype.JobStateRetryable,
				rivertype.JobStateScheduled,
			},
		},
	}
}

type CleanupArgs struct{}

func (CleanupArgs) Kind() string { return cleanupJobKind }

func (CleanupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: maxCleanupAttempts, Queue: JobQueue}
}

func PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(cleanupInterval),
			func() (river.JobArgs, *river.InsertOpts) {
				return CleanupArgs{}, &river.InsertOpts{
					MaxAttempts: maxCleanupAttempts,
					Queue:       JobQueue,
					UniqueOpts:  river.UniqueOpts{ByArgs: true, ByPeriod: cleanupInterval},
				}
			},
			&river.PeriodicJobOpts{RunOnStart: true},
		),
	}
}

type JobHandler interface {
	Process(ctx context.Context, args JobArgs) error
}

type Worker struct {
	river.WorkerDefaults[JobArgs]
	handler JobHandler
}

func NewWorker(handler JobHandler) *Worker {
	return &Worker{handler: handler}
}

func (w *Worker) Timeout(*river.Job[JobArgs]) time.Duration { return jobTimeout }

func (w *Worker) Work(ctx context.Context, job *river.Job[JobArgs]) error {
	err := w.handler.Process(ctx, job.Args)
	var permanent interface{ Permanent() bool }
	if errors.As(err, &permanent) && permanent.Permanent() {
		return river.JobCancel(err)
	}
	return err
}

type CleanupHandler interface {
	Cleanup(ctx context.Context) error
}

type CleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	handler CleanupHandler
}

func NewCleanupWorker(handler CleanupHandler) *CleanupWorker {
	return &CleanupWorker{handler: handler}
}

func (w *CleanupWorker) Timeout(*river.Job[CleanupArgs]) time.Duration { return cleanupJobTimeout }

func (w *CleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	return w.handler.Cleanup(ctx)
}
