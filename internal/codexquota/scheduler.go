package codexquota

import (
	"context"
	"errors"
	"time"

	log "github.com/sirupsen/logrus"
)

// ClockTime is a wall-clock trigger time.
type ClockTime struct {
	Hour   int
	Minute int
}

// DefaultTimes are the UTC+8 quota warmer trigger times.
var DefaultTimes = []ClockTime{
	{Hour: 6, Minute: 40},
	{Hour: 11, Minute: 45},
	{Hour: 17, Minute: 0},
}

// UTC8 returns the fixed UTC+8 timezone used by the schedule.
func UTC8() *time.Location {
	return time.FixedZone("UTC+8", 8*60*60)
}

// StartScheduler starts the background Codex quota warmer scheduler.
func StartScheduler(ctx context.Context, manager AuthManager) {
	runner := NewRunner(manager)
	go runSchedule(ctx, runner, UTC8(), DefaultTimes, time.Now)
}

func runSchedule(ctx context.Context, runner *Runner, loc *time.Location, times []ClockTime, now func() time.Time) {
	if ctx == nil {
		ctx = context.Background()
	}
	if loc == nil {
		loc = UTC8()
	}
	if len(times) == 0 {
		times = DefaultTimes
	}
	if now == nil {
		now = time.Now
	}
	for {
		next := NextRunAfter(now(), loc, times)
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			go func() {
				summary, err := runner.TryRun(ctx, nil)
				if err != nil {
					if errors.Is(err, ErrAlreadyRunning) {
						log.Warn("codex quota warmer skipped because a previous run is still active")
						return
					}
					if errors.Is(err, context.Canceled) {
						return
					}
					log.Warnf("codex quota warmer failed: %v", err)
					return
				}
				log.Infof("codex quota warmer completed: total=%d success=%d failed=%d", summary.Total, summary.Success, summary.Failed)
			}()
		}
	}
}

// NextRunAfter returns the next scheduled time after now.
func NextRunAfter(now time.Time, loc *time.Location, times []ClockTime) time.Time {
	if loc == nil {
		loc = UTC8()
	}
	if len(times) == 0 {
		times = DefaultTimes
	}
	localNow := now.In(loc)
	ordered := append([]ClockTime(nil), times...)
	sortClockTimes(ordered)
	for _, t := range ordered {
		candidate := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), t.Hour, t.Minute, 0, 0, loc)
		if candidate.After(localNow) {
			return candidate
		}
	}
	first := ordered[0]
	return time.Date(localNow.Year(), localNow.Month(), localNow.Day()+1, first.Hour, first.Minute, 0, 0, loc)
}

func sortClockTimes(times []ClockTime) {
	for i := 1; i < len(times); i++ {
		current := times[i]
		j := i - 1
		for j >= 0 && clockTimeLess(current, times[j]) {
			times[j+1] = times[j]
			j--
		}
		times[j+1] = current
	}
}

func clockTimeLess(a, b ClockTime) bool {
	if a.Hour != b.Hour {
		return a.Hour < b.Hour
	}
	return a.Minute < b.Minute
}
