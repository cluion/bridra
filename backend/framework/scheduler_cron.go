package framework

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const cronSearchYears = 8

var cronMonthNames = map[string]int{
	"JAN": 1,
	"FEB": 2,
	"MAR": 3,
	"APR": 4,
	"MAY": 5,
	"JUN": 6,
	"JUL": 7,
	"AUG": 8,
	"SEP": 9,
	"OCT": 10,
	"NOV": 11,
	"DEC": 12,
}

var cronWeekdayNames = map[string]int{
	"SUN": 0,
	"MON": 1,
	"TUE": 2,
	"WED": 3,
	"THU": 4,
	"FRI": 5,
	"SAT": 6,
}

type cronSchedule struct {
	expression cronExpression
	location   *time.Location
}

func (schedule cronSchedule) nextDelay(now time.Time) (time.Duration, bool) {
	next, ok := schedule.expression.next(now, schedule.location)
	if !ok {
		return 0, false
	}
	return next.Sub(now), true
}

type cronExpression struct {
	minutes         cronField
	hours           cronField
	daysOfMonth     cronField
	months          cronField
	weekdays        cronField
	dayWildcard     bool
	weekdayWildcard bool
}

type cronField struct {
	allowed []bool
}

func ScheduleCronTask(
	scheduler *Scheduler,
	name string,
	expression string,
	task ScheduledTask,
) error {
	if scheduler == nil {
		return ErrSchedulerUnavailable
	}
	name = strings.TrimSpace(name)
	if name == "" || task == nil {
		return ErrInvalidScheduledTask
	}

	parsed, err := parseCronExpression(expression)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCronExpression, err)
	}
	schedule := cronSchedule{
		expression: parsed,
		location:   scheduler.options.Location,
	}
	if _, ok := schedule.nextDelay(scheduler.clock.Now()); !ok {
		return fmt.Errorf("%w: expression has no reachable occurrence", ErrInvalidCronExpression)
	}
	return scheduler.registerTask(name, schedule, task)
}

func parseCronExpression(value string) (cronExpression, error) {
	parts := strings.Fields(value)
	if len(parts) != 5 {
		return cronExpression{}, fmt.Errorf("expected 5 fields")
	}

	minutes, err := parseCronField(parts[0], 0, 59, nil)
	if err != nil {
		return cronExpression{}, fmt.Errorf("minute: %w", err)
	}
	hours, err := parseCronField(parts[1], 0, 23, nil)
	if err != nil {
		return cronExpression{}, fmt.Errorf("hour: %w", err)
	}
	daysOfMonth, err := parseCronField(parts[2], 1, 31, nil)
	if err != nil {
		return cronExpression{}, fmt.Errorf("day of month: %w", err)
	}
	months, err := parseCronField(parts[3], 1, 12, cronMonthNames)
	if err != nil {
		return cronExpression{}, fmt.Errorf("month: %w", err)
	}
	weekdays, err := parseCronField(parts[4], 0, 7, cronWeekdayNames)
	if err != nil {
		return cronExpression{}, fmt.Errorf("day of week: %w", err)
	}
	weekdays.allowed[0] = weekdays.allowed[0] || weekdays.allowed[7]
	weekdays.allowed = weekdays.allowed[:7]

	return cronExpression{
		minutes:         minutes,
		hours:           hours,
		daysOfMonth:     daysOfMonth,
		months:          months,
		weekdays:        weekdays,
		dayWildcard:     daysOfMonth.allowsEvery(1, 31),
		weekdayWildcard: weekdays.allowsEvery(0, 6),
	}, nil
}

func (field cronField) allowsEvery(minimum int, maximum int) bool {
	for value := minimum; value <= maximum; value++ {
		if !field.allowed[value] {
			return false
		}
	}
	return true
}

func parseCronField(
	value string,
	minimum int,
	maximum int,
	names map[string]int,
) (cronField, error) {
	field := cronField{allowed: make([]bool, maximum+1)}
	for _, item := range strings.Split(strings.ToUpper(value), ",") {
		if item == "" {
			return cronField{}, fmt.Errorf("empty list item")
		}
		base, step, hasStep, err := parseCronStep(item)
		if err != nil {
			return cronField{}, err
		}

		start := minimum
		end := maximum
		switch {
		case base == "*":
		case strings.Contains(base, "-"):
			bounds := strings.Split(base, "-")
			if len(bounds) != 2 || bounds[0] == "" || bounds[1] == "" {
				return cronField{}, fmt.Errorf("invalid range %q", base)
			}
			start, err = parseCronValue(bounds[0], minimum, maximum, names)
			if err != nil {
				return cronField{}, err
			}
			end, err = parseCronValue(bounds[1], minimum, maximum, names)
			if err != nil {
				return cronField{}, err
			}
			if start > end {
				return cronField{}, fmt.Errorf("range %q is reversed", base)
			}
		default:
			start, err = parseCronValue(base, minimum, maximum, names)
			if err != nil {
				return cronField{}, err
			}
			end = start
			if hasStep {
				end = maximum
			}
		}

		for current := start; current <= end; current += step {
			field.allowed[current] = true
		}
	}
	return field, nil
}

func parseCronStep(value string) (string, int, bool, error) {
	parts := strings.Split(value, "/")
	if len(parts) > 2 || parts[0] == "" {
		return "", 0, false, fmt.Errorf("invalid step %q", value)
	}
	if len(parts) == 1 {
		return parts[0], 1, false, nil
	}
	step, err := strconv.Atoi(parts[1])
	if err != nil || step <= 0 {
		return "", 0, false, fmt.Errorf("invalid step %q", parts[1])
	}
	return parts[0], step, true, nil
}

func parseCronValue(
	value string,
	minimum int,
	maximum int,
	names map[string]int,
) (int, error) {
	if named, ok := names[value]; ok {
		return named, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("value %q is outside %d-%d", value, minimum, maximum)
	}
	return parsed, nil
}

func (expression cronExpression) next(
	after time.Time,
	location *time.Location,
) (time.Time, bool) {
	if location == nil {
		location = time.Local
	}
	candidate := after.In(location).Truncate(time.Minute).Add(time.Minute)
	limit := candidate.AddDate(cronSearchYears, 0, 0)

	for !candidate.After(limit) {
		if !expression.months.allowed[int(candidate.Month())] {
			candidate = time.Date(
				candidate.Year(),
				candidate.Month()+1,
				1,
				0,
				0,
				0,
				0,
				location,
			)
			continue
		}
		if !expression.matchesDay(candidate) {
			candidate = time.Date(
				candidate.Year(),
				candidate.Month(),
				candidate.Day()+1,
				0,
				0,
				0,
				0,
				location,
			)
			continue
		}
		if !expression.hours.allowed[candidate.Hour()] {
			candidate = candidate.Add(
				time.Duration(60-candidate.Minute()) * time.Minute,
			)
			continue
		}
		if !expression.minutes.allowed[candidate.Minute()] {
			candidate = candidate.Add(time.Minute)
			continue
		}
		return candidate, true
	}
	return time.Time{}, false
}

func (expression cronExpression) matchesDay(candidate time.Time) bool {
	dayMatches := expression.daysOfMonth.allowed[candidate.Day()]
	weekdayMatches := expression.weekdays.allowed[int(candidate.Weekday())]
	switch {
	case expression.dayWildcard:
		return weekdayMatches
	case expression.weekdayWildcard:
		return dayMatches
	default:
		return dayMatches || weekdayMatches
	}
}
