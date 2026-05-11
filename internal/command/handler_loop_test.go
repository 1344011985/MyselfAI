package command

import "testing"

func TestInferPlanScheduleIntervalHours(t *testing.T) {
	schedule := inferPlanSchedule("小说连载，每 2 小时更新一次")

	if schedule.Kind != "interval" {
		t.Fatalf("expected interval schedule, got %q", schedule.Kind)
	}
	if schedule.IntervalMinutes != 120 {
		t.Fatalf("expected 120 interval minutes, got %d", schedule.IntervalMinutes)
	}
}

func TestInferPlanScheduleIntervalMinutes(t *testing.T) {
	schedule := inferPlanSchedule("每 30 分钟检查一次")

	if schedule.Kind != "interval" {
		t.Fatalf("expected interval schedule, got %q", schedule.Kind)
	}
	if schedule.IntervalMinutes != 30 {
		t.Fatalf("expected 30 interval minutes, got %d", schedule.IntervalMinutes)
	}
}
