package framework

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCronExpressionFindsNextOccurrence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		expression string
		after      time.Time
		want       time.Time
	}{
		{
			name:       "steps ranges and weekday names",
			expression: "*/15 9-17 * * MON-FRI",
			after:      time.Date(2026, time.July, 27, 8, 7, 0, 0, time.UTC),
			want:       time.Date(2026, time.July, 27, 9, 0, 0, 0, time.UTC),
		},
		{
			name:       "next weekday after business hours",
			expression: "*/15 9-17 * * MON-FRI",
			after:      time.Date(2026, time.July, 31, 17, 59, 0, 0, time.UTC),
			want:       time.Date(2026, time.August, 3, 9, 0, 0, 0, time.UTC),
		},
		{
			name:       "month list and names",
			expression: "30 6 1 JAN,JUL *",
			after:      time.Date(2026, time.January, 1, 6, 30, 0, 0, time.UTC),
			want:       time.Date(2026, time.July, 1, 6, 30, 0, 0, time.UTC),
		},
		{
			name:       "seven is Sunday",
			expression: "0 8 * * 7",
			after:      time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC),
			want:       time.Date(2026, time.July, 26, 8, 0, 0, 0, time.UTC),
		},
		{
			name:       "leap day",
			expression: "0 0 29 FEB *",
			after:      time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC),
			want:       time.Date(2028, time.February, 29, 0, 0, 0, 0, time.UTC),
		},
		{
			name:       "day of month or weekday",
			expression: "0 9 15 * MON",
			after:      time.Date(2026, time.July, 14, 10, 0, 0, 0, time.UTC),
			want:       time.Date(2026, time.July, 15, 9, 0, 0, 0, time.UTC),
		},
		{
			name:       "weekday can satisfy restricted day pair",
			expression: "0 9 15 * MON",
			after:      time.Date(2026, time.July, 15, 9, 0, 0, 0, time.UTC),
			want:       time.Date(2026, time.July, 20, 9, 0, 0, 0, time.UTC),
		},
		{
			name:       "full day range is unrestricted",
			expression: "0 9 */1 * MON",
			after:      time.Date(2026, time.July, 14, 10, 0, 0, 0, time.UTC),
			want:       time.Date(2026, time.July, 20, 9, 0, 0, 0, time.UTC),
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			expression, err := parseCronExpression(test.expression)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			got, ok := expression.next(test.after, time.UTC)
			if !ok {
				t.Fatal("next occurrence was not found")
			}
			if !got.Equal(test.want) {
				t.Fatalf("next = %v, want %v", got, test.want)
			}
		})
	}
}

func TestSchedulerCronUsesConfiguredLocation(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	clock := newControlledSchedulerClock()
	clock.SetNow(time.Date(2026, time.July, 27, 0, 30, 0, 0, time.UTC))
	scheduler, err := newScheduler(SchedulerOptions{Location: location}, clock)
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}
	if err := ScheduleCronTask(
		scheduler,
		"local.morning",
		"0 9 * * *",
		func(context.Context) error { return nil },
	); err != nil {
		t.Fatalf("schedule cron task: %v", err)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	timer := nextControlledTimer(t, clock)
	if timer.delay != 30*time.Minute {
		t.Fatalf("timer delay = %v, want 30m", timer.delay)
	}
	if err := scheduler.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestScheduleCronTaskRejectsInvalidDefinitions(t *testing.T) {
	t.Parallel()
	clock := newControlledSchedulerClock()
	scheduler, err := newScheduler(SchedulerOptions{Location: time.UTC}, clock)
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}
	task := ScheduledTask(func(context.Context) error { return nil })

	if err := ScheduleCronTask(nil, "task", "* * * * *", task); !errors.Is(
		err,
		ErrSchedulerUnavailable,
	) {
		t.Fatalf("nil scheduler error = %v", err)
	}
	for _, test := range []struct {
		name       string
		expression string
		task       ScheduledTask
		want       error
	}{
		{name: "", expression: "* * * * *", task: task, want: ErrInvalidScheduledTask},
		{name: "nil", expression: "* * * * *", task: nil, want: ErrInvalidScheduledTask},
		{name: "fields", expression: "* * * *", task: task, want: ErrInvalidCronExpression},
		{name: "minute", expression: "60 * * * *", task: task, want: ErrInvalidCronExpression},
		{name: "hour", expression: "* 24 * * *", task: task, want: ErrInvalidCronExpression},
		{name: "day", expression: "* * 0 * *", task: task, want: ErrInvalidCronExpression},
		{name: "month", expression: "* * * 13 *", task: task, want: ErrInvalidCronExpression},
		{name: "weekday", expression: "* * * * 8", task: task, want: ErrInvalidCronExpression},
		{name: "step", expression: "*/0 * * * *", task: task, want: ErrInvalidCronExpression},
		{name: "range", expression: "10-5 * * * *", task: task, want: ErrInvalidCronExpression},
		{name: "list", expression: "1,,2 * * * *", task: task, want: ErrInvalidCronExpression},
		{name: "macro", expression: "@daily", task: task, want: ErrInvalidCronExpression},
		{name: "unreachable", expression: "0 0 30 FEB *", task: task, want: ErrInvalidCronExpression},
	} {
		if err := ScheduleCronTask(
			scheduler,
			test.name,
			test.expression,
			test.task,
		); !errors.Is(err, test.want) {
			t.Fatalf("%q error = %v, want %v", test.name, err, test.want)
		}
	}
}

func TestSchedulerRunsCronTaskAtNextWallClockOccurrence(t *testing.T) {
	clock := newControlledSchedulerClock()
	clock.SetNow(time.Date(2026, time.July, 27, 8, 7, 0, 0, time.UTC))
	scheduler, err := newScheduler(SchedulerOptions{Location: time.UTC}, clock)
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}
	t.Cleanup(func() {
		_ = scheduler.Shutdown(context.Background())
	})

	ran := make(chan struct{}, 1)
	if err := ScheduleCronTask(
		scheduler,
		"reports.daily",
		"15 9 * * *",
		func(context.Context) error {
			ran <- struct{}{}
			return nil
		},
	); err != nil {
		t.Fatalf("schedule cron task: %v", err)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	first := nextControlledTimer(t, clock)
	if first.delay != time.Hour+8*time.Minute {
		t.Fatalf("first delay = %v", first.delay)
	}
	if !first.Fire() {
		t.Fatal("first timer did not fire")
	}
	select {
	case <-ran:
	case <-time.After(time.Second):
		t.Fatal("cron task did not run")
	}
	second := nextControlledTimer(t, clock)
	if second.delay != 24*time.Hour {
		t.Fatalf("second delay = %v", second.delay)
	}
}

func TestSchedulerSkipsCronOccurrencesMissedWhileTaskRuns(t *testing.T) {
	clock := newControlledSchedulerClock()
	clock.SetNow(time.Date(2026, time.July, 27, 8, 0, 0, 0, time.UTC))
	scheduler, err := newScheduler(SchedulerOptions{Location: time.UTC}, clock)
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}
	release := make(chan struct{})
	started := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		_ = scheduler.Shutdown(context.Background())
	})

	if err := ScheduleCronTask(
		scheduler,
		"reports.hourly",
		"0 * * * *",
		func(context.Context) error {
			close(started)
			<-release
			return nil
		},
	); err != nil {
		t.Fatalf("schedule cron task: %v", err)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	first := nextControlledTimer(t, clock)
	if first.delay != time.Hour || !first.Fire() {
		t.Fatalf("first timer = %#v", first)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("cron task did not start")
	}

	clock.SetNow(time.Date(2026, time.July, 27, 11, 30, 0, 0, time.UTC))
	close(release)
	second := nextControlledTimer(t, clock)
	if second.delay != 30*time.Minute {
		t.Fatalf("next delay = %v, want 30m", second.delay)
	}
}

func TestScheduleCronTaskRejectsDuplicateAndLateRegistration(t *testing.T) {
	t.Parallel()
	clock := newControlledSchedulerClock()
	scheduler, err := newScheduler(SchedulerOptions{Location: time.UTC}, clock)
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}
	task := ScheduledTask(func(context.Context) error { return nil })
	if err := ScheduleCronTask(scheduler, "task", "* * * * *", task); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if err := ScheduleCronTask(scheduler, "task", "* * * * *", task); !errors.Is(
		err,
		ErrScheduledTaskAlreadyDefined,
	) {
		t.Fatalf("duplicate error = %v", err)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := ScheduleCronTask(scheduler, "late", "* * * * *", task); !errors.Is(
		err,
		ErrScheduledTaskRegistrationClosed,
	) {
		t.Fatalf("late registration error = %v", err)
	}
	if err := scheduler.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}
