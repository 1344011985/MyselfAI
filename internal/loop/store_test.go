package loop

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestScheduleCRUD(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	nextRun := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)

	schedule := &LoopSchedule{
		UserID:       "user-1",
		GroupID:      "group-1",
		Title:        "Daily Runtime Check",
		ProjectKey:   "myselfclow",
		Goal:         "Check project status and plan one safe step.",
		ScheduleExpr: "daily:09:00",
		Executor:     "codex",
		PlanJSON:     `{"title":"Daily Runtime Check"}`,
		NextRunAt:    &nextRun,
	}
	if err := store.CreateSchedule(ctx, schedule); err != nil {
		t.Fatalf("CreateSchedule failed: %v", err)
	}
	if !strings.HasPrefix(schedule.ID, "loop_") {
		t.Fatalf("schedule id should be generated, got %q", schedule.ID)
	}

	got, err := store.GetSchedule(ctx, schedule.ID)
	if err != nil {
		t.Fatalf("GetSchedule failed: %v", err)
	}
	if got.Status != ScheduleStatusActive {
		t.Fatalf("default schedule status = %q, want %q", got.Status, ScheduleStatusActive)
	}
	if got.SafetyProfile != SafetyProfileConservative {
		t.Fatalf("default safety profile = %q, want %q", got.SafetyProfile, SafetyProfileConservative)
	}
	if got.Timezone != "Asia/Shanghai" {
		t.Fatalf("default timezone = %q", got.Timezone)
	}
	if got.MaxRunsPerDay != 100 || got.MaxChildTasks != 3 {
		t.Fatalf("unexpected default limits: runs=%d children=%d", got.MaxRunsPerDay, got.MaxChildTasks)
	}
	if got.NextRunAt == nil || !got.NextRunAt.Equal(nextRun) {
		t.Fatalf("next run mismatch: got %v want %v", got.NextRunAt, nextRun)
	}

	list, err := store.ListSchedules(ctx, "user-1", 10)
	if err != nil {
		t.Fatalf("ListSchedules failed: %v", err)
	}
	if len(list) != 1 || list[0].ID != schedule.ID {
		t.Fatalf("unexpected schedule list: %#v", list)
	}

	if err := store.UpdateScheduleStatus(ctx, schedule.ID, ScheduleStatusPaused); err != nil {
		t.Fatalf("UpdateScheduleStatus failed: %v", err)
	}
	got, err = store.GetSchedule(ctx, schedule.ID)
	if err != nil {
		t.Fatalf("GetSchedule after update failed: %v", err)
	}
	if got.Status != ScheduleStatusPaused {
		t.Fatalf("status after update = %q, want %q", got.Status, ScheduleStatusPaused)
	}
}

func TestRunAndEventCRUD(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	schedule := &LoopSchedule{
		UserID:       "user-1",
		Title:        "Manual Runtime Check",
		Goal:         "Check project state.",
		ScheduleExpr: "manual",
	}
	if err := store.CreateSchedule(ctx, schedule); err != nil {
		t.Fatalf("CreateSchedule failed: %v", err)
	}

	run := &LoopRun{
		ScheduleID:  schedule.ID,
		TriggerType: TriggerTypeManual,
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}
	if !strings.HasPrefix(run.ID, "run_") {
		t.Fatalf("run id should be generated, got %q", run.ID)
	}

	if err := store.UpdateRunParentTask(ctx, run.ID, "task_parent"); err != nil {
		t.Fatalf("UpdateRunParentTask failed: %v", err)
	}
	if err := store.UpdateRunStatus(ctx, run.ID, RunStatusDone, "checked", "none", ""); err != nil {
		t.Fatalf("UpdateRunStatus failed: %v", err)
	}
	gotRun, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetRun failed: %v", err)
	}
	if gotRun.Status != RunStatusDone || gotRun.ParentTaskID != "task_parent" {
		t.Fatalf("unexpected run after update: %#v", gotRun)
	}
	if gotRun.ResultSummary != "checked" || gotRun.NextAction != "none" {
		t.Fatalf("unexpected run summary/action: %#v", gotRun)
	}
	if gotRun.DoneAt == nil {
		t.Fatalf("done run should have done_at")
	}

	events := []*LoopEvent{
		{RunID: run.ID, ScheduleID: schedule.ID, EventType: EventTypeCreated, Message: "run created"},
		{RunID: run.ID, ScheduleID: schedule.ID, TaskID: "task_parent", EventType: EventTypeParentTaskSubmitted, Message: "parent task submitted"},
	}
	for _, event := range events {
		if err := store.AppendEvent(ctx, event); err != nil {
			t.Fatalf("AppendEvent failed: %v", err)
		}
		if event.ID == 0 {
			t.Fatalf("event id should be populated")
		}
	}
	gotEvents, err := store.ListEvents(ctx, run.ID, 10)
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	if len(gotEvents) != 2 {
		t.Fatalf("got %d events, want 2", len(gotEvents))
	}
	if gotEvents[0].EventType != EventTypeCreated || gotEvents[1].TaskID != "task_parent" {
		t.Fatalf("unexpected events order/content: %#v", gotEvents)
	}
}

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	return store
}
