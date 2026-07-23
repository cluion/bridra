package services

import (
	"testing"
	"time"
)

func TestGreetingServiceUsesDefaultNameAndUTCClock(t *testing.T) {
	service := NewGreetingServiceWithClock(func() time.Time {
		return time.Date(2026, time.July, 20, 20, 30, 0, 0, time.FixedZone("test", 8*60*60))
	})

	greeting := service.Greet("   ")

	if greeting.Message != "Hello, Flutter!" {
		t.Fatalf("message = %q", greeting.Message)
	}
	want := time.Date(2026, time.July, 20, 12, 30, 0, 0, time.UTC)
	if !greeting.Timestamp.Equal(want) {
		t.Fatalf("timestamp = %v", greeting.Timestamp)
	}
}

func TestGreetingServiceTrimsNames(t *testing.T) {
	service := NewGreetingServiceWithClock(func() time.Time { return time.Time{} })

	if greeting := service.Greet("  Codex  "); greeting.Message != "Hello, Codex!" {
		t.Fatalf("message = %q", greeting.Message)
	}
}
