package scheduler

import (
	"testing"
	"time"
)

func TestParseClock(t *testing.T) {
	clock, err := ParseClock("03:05")
	if err != nil {
		t.Fatal(err)
	}
	if clock.Hour != 3 || clock.Minute != 5 {
		t.Fatalf("clock = %#v, want 03:05", clock)
	}

	for _, value := range []string{"", "3", "24:00", "03:60", "aa:00"} {
		if _, err := ParseClock(value); err == nil {
			t.Fatalf("ParseClock(%q) succeeded, want error", value)
		}
	}
}

func TestNextTime(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	clock := Clock{Hour: 3}

	now := time.Date(2026, 7, 9, 2, 0, 0, 0, loc)
	want := time.Date(2026, 7, 9, 3, 0, 0, 0, loc)
	if got := nextTime(now, clock, loc); !got.Equal(want) {
		t.Fatalf("nextTime before schedule = %s, want %s", got, want)
	}

	now = time.Date(2026, 7, 9, 3, 0, 0, 0, loc)
	want = time.Date(2026, 7, 10, 3, 0, 0, 0, loc)
	if got := nextTime(now, clock, loc); !got.Equal(want) {
		t.Fatalf("nextTime at schedule = %s, want %s", got, want)
	}
}

func TestPreviousDateUsesScheduleTimezone(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 9, 0, 30, 0, 0, loc)
	if got := previousDate(now, loc); got != "2026-07-08" {
		t.Fatalf("previousDate() = %q, want %q", got, "2026-07-08")
	}
}
