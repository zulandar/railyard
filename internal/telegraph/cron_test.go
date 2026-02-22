package telegraph

import (
	"testing"
	"time"
)

func TestNextCronDuration_ValidExpression(t *testing.T) {
	// "0 9 * * *" = daily at 09:00. Duration should be positive and < 24h.
	d := nextCronDuration("0 9 * * *")
	if d <= 0 {
		t.Fatalf("expected positive duration, got %v", d)
	}
	if d > 24*time.Hour {
		t.Fatalf("expected duration < 24h, got %v", d)
	}
}

func TestNextCronDuration_InvalidExpression(t *testing.T) {
	d := nextCronDuration("not a cron expr")
	if d != 0 {
		t.Fatalf("expected 0 for invalid expression, got %v", d)
	}
}

func TestNextCronDuration_EveryMinute(t *testing.T) {
	// "* * * * *" = every minute. Duration should be < 61s.
	d := nextCronDuration("* * * * *")
	if d <= 0 {
		t.Fatalf("expected positive duration, got %v", d)
	}
	if d > 61*time.Second {
		t.Fatalf("expected duration < 61s, got %v", d)
	}
}
