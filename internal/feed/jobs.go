package feed

import (
	"time"

	"github.com/riverqueue/river"
)

const (
	refreshJobKind         = "feed.refresh"
	scheduleRefreshJobKind = "feed.refresh.schedule"
	RefreshScheduleQueue   = "spool-scheduler"

	maxRefreshAttempts     = 8
	defaultJobTimeout      = 30 * time.Second
	defaultRefreshInterval = 15 * time.Minute
	scheduleRefreshEvery   = time.Minute
	scheduleRefreshBatch   = 500
	maxScheduleAttempts    = 4
)

type RefreshArgs struct {
	FeedID     string `json:"feed_id"`
	FeedURL    string `json:"feed_url"`
	Generation int64  `json:"generation"`
}

func (RefreshArgs) Kind() string { return refreshJobKind }

func (RefreshArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: maxRefreshAttempts,
		UniqueOpts:  river.UniqueOpts{ByArgs: true},
	}
}

type ScheduleRefreshArgs struct{}

func (ScheduleRefreshArgs) Kind() string { return scheduleRefreshJobKind }

func (ScheduleRefreshArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: maxScheduleAttempts}
}

func PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(scheduleRefreshEvery),
			func() (river.JobArgs, *river.InsertOpts) {
				return ScheduleRefreshArgs{}, &river.InsertOpts{
					MaxAttempts: maxScheduleAttempts,
					Queue:       RefreshScheduleQueue,
					UniqueOpts:  river.UniqueOpts{ByArgs: true, ByPeriod: scheduleRefreshEvery},
				}
			},
			&river.PeriodicJobOpts{RunOnStart: true},
		),
	}
}

func refreshArgs(feedID, feedURL string, refreshedAt *time.Time) RefreshArgs {
	args := RefreshArgs{FeedID: feedID, FeedURL: feedURL}
	if refreshedAt != nil {
		args.Generation = refreshedAt.UnixNano()
	}
	return args
}
