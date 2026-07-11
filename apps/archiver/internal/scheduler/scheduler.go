// Package scheduler runs a daily job at a configured local time.
package scheduler

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// Config controls when the daemon runs the daily archive.
type Config struct {
	Time       string // HH:MM in 24-hour time
	Timezone   string // IANA timezone name
	RunOnStart bool
}

type Clock struct {
	Hour   int
	Minute int
}

func ParseClock(value string) (Clock, error) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 2 {
		return Clock{}, fmt.Errorf("parse schedule time %q: expected HH:MM", value)
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return Clock{}, fmt.Errorf("parse schedule hour %q: %w", parts[0], err)
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return Clock{}, fmt.Errorf("parse schedule minute %q: %w", parts[1], err)
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return Clock{}, fmt.Errorf("parse schedule time %q: expected HH:MM in 24-hour time", value)
	}
	return Clock{Hour: hour, Minute: minute}, nil
}

// Run archives the previous day at the configured time until ctx is done.
// archive receives the target date in YYYY-MM-DD.
func Run(ctx context.Context, config Config, archive func(date string) error) error {
	loc, err := time.LoadLocation(config.Timezone)
	if err != nil {
		return fmt.Errorf("load schedule timezone %q: %w", config.Timezone, err)
	}
	clock, err := ParseClock(config.Time)
	if err != nil {
		return err
	}

	if config.RunOnStart {
		runArchive(archive, previousDate(time.Now().In(loc), loc))
	}

	for {
		next := nextTime(time.Now().In(loc), clock, loc)
		log.Printf("next scheduled archive at %s", next.Format(time.RFC3339))
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
			runArchive(archive, previousDate(time.Now().In(loc), loc))
		}
	}
}

func runArchive(archive func(date string) error, date string) {
	log.Printf("starting scheduled archive for %s", date)
	if err := archive(date); err != nil {
		log.Printf("scheduled archive for %s failed: %v", date, err)
		return
	}
	log.Printf("finished scheduled archive for %s", date)
}

func nextTime(now time.Time, clock Clock, loc *time.Location) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), clock.Hour, clock.Minute, 0, 0, loc)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func previousDate(now time.Time, loc *time.Location) string {
	return now.In(loc).AddDate(0, 0, -1).Format("2006-01-02")
}
