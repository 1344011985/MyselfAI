package loop

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type SchedulerLogger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

type Scheduler struct {
	store    Store
	runner   *Runner
	log      SchedulerLogger
	interval time.Duration
	limit    int
}

func NewScheduler(store Store, runner *Runner, log SchedulerLogger) *Scheduler {
	return &Scheduler{
		store:    store,
		runner:   runner,
		log:      log,
		interval: time.Minute,
		limit:    20,
	}
}

func (s *Scheduler) Start(ctx context.Context) {
	if s == nil || s.store == nil || s.runner == nil {
		return
	}
	s.info("loop scheduler started", "interval", s.interval.String())
	s.RunOnce(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.info("loop scheduler stopped")
			return
		case <-ticker.C:
			s.RunOnce(ctx)
		}
	}
}

func (s *Scheduler) RunOnce(ctx context.Context) {
	now := time.Now().UTC()
	schedules, err := s.store.ListDueSchedules(ctx, now, s.limit)
	if err != nil {
		s.error("loop scheduler scan failed", "err", err)
		return
	}
	for _, schedule := range schedules {
		if ctx.Err() != nil {
			return
		}
		s.trigger(ctx, schedule, now)
	}
}

func (s *Scheduler) trigger(ctx context.Context, schedule *LoopSchedule, now time.Time) {
	if schedule == nil {
		return
	}
	if s.hasActiveRun(ctx, schedule.ID) {
		s.info("loop scheduler skipped active run", "loop_id", schedule.ID)
		return
	}

	nextRunAt, err := NextRunAfter(schedule.ScheduleExpr, schedule.Timezone, now)
	if err != nil {
		s.warn("loop scheduler cannot compute next run", "loop_id", schedule.ID, "expr", schedule.ScheduleExpr, "err", err)
		nextRunAt = nil
	}
	claimed, err := s.store.ClaimDueSchedule(ctx, schedule.ID, now, nextRunAt)
	if err != nil {
		s.error("loop scheduler claim failed", "loop_id", schedule.ID, "err", err)
		return
	}
	if !claimed {
		return
	}

	run, task, err := s.runner.RunScheduled(ctx, schedule.ID)
	if err != nil {
		if errors.Is(err, ErrActiveRun) {
			s.info("loop scheduler skipped duplicate active run", "loop_id", schedule.ID)
			return
		}
		s.error("loop scheduler run failed", "loop_id", schedule.ID, "err", err)
		return
	}
	_ = s.store.AppendEvent(ctx, &LoopEvent{
		RunID:      run.ID,
		ScheduleID: schedule.ID,
		TaskID:     task.ID,
		EventType:  EventTypeDueClaimed,
		Message:    "scheduled loop run claimed",
	})
	s.info("loop scheduler submitted run", "loop_id", schedule.ID, "run_id", run.ID, "task_id", task.ID)
}

func (s *Scheduler) hasActiveRun(ctx context.Context, scheduleID string) bool {
	runs, err := s.store.ListRuns(ctx, scheduleID, 5)
	if err != nil {
		s.warn("loop scheduler active-run check failed", "loop_id", scheduleID, "err", err)
		return true
	}
	for _, run := range runs {
		if run.Status == RunStatusPending || run.Status == RunStatusRunning {
			return true
		}
	}
	return false
}

func NextRunAfter(expr, timezone string, after time.Time) (*time.Time, error) {
	expr = strings.TrimSpace(strings.ToLower(expr))
	if expr == "" || expr == "manual" {
		return nil, nil
	}
	expr = strings.TrimSpace(strings.Split(expr, "+")[0])
	if expr == "" || expr == "manual" || expr == "task_done" {
		return nil, nil
	}
	loc := loadScheduleLocation(timezone)
	switch {
	case strings.HasPrefix(expr, "daily:"):
		return nextDailyRun(strings.TrimPrefix(expr, "daily:"), loc, after)
	case strings.HasPrefix(expr, "interval:"):
		return nextIntervalRun(strings.TrimPrefix(expr, "interval:"), after)
	default:
		return nil, fmt.Errorf("unsupported schedule expression %q", expr)
	}
}

func nextDailyRun(value string, loc *time.Location, after time.Time) (*time.Time, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid daily time %q", value)
	}
	hour, hourErr := strconv.Atoi(parts[0])
	minute, minuteErr := strconv.Atoi(parts[1])
	if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return nil, fmt.Errorf("invalid daily time %q", value)
	}
	localAfter := after.In(loc)
	next := time.Date(localAfter.Year(), localAfter.Month(), localAfter.Day(), hour, minute, 0, 0, loc)
	if !next.After(localAfter) {
		next = next.Add(24 * time.Hour)
	}
	return &next, nil
}

func nextIntervalRun(value string, after time.Time) (*time.Time, error) {
	duration, err := time.ParseDuration(value)
	if err != nil {
		return nil, err
	}
	if duration <= 0 {
		return nil, fmt.Errorf("interval must be positive")
	}
	next := after.Add(duration)
	return &next, nil
}

func loadScheduleLocation(timezone string) *time.Location {
	if strings.TrimSpace(timezone) == "" {
		timezone = "Asia/Shanghai"
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	return loc
}

func HasTaskDoneTrigger(schedule *LoopSchedule) bool {
	if schedule == nil {
		return false
	}
	return strings.Contains(strings.ToLower(schedule.ScheduleExpr), "task_done")
}

func (s *Scheduler) info(msg string, args ...any) {
	if s.log != nil {
		s.log.Info(msg, args...)
	}
}

func (s *Scheduler) warn(msg string, args ...any) {
	if s.log != nil {
		s.log.Warn(msg, args...)
	}
}

func (s *Scheduler) error(msg string, args ...any) {
	if s.log != nil {
		s.log.Error(msg, args...)
	}
}
