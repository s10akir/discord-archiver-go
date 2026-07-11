package cli

import (
	"testing"

	"github.com/s10akir/discord-archiver-go/apps/archiver/internal/archive"
)

func TestParseDefaultsToDaemon(t *testing.T) {
	config, err := Parse(nil, testEnv(map[string]string{
		"DISCORD_BOT_TOKEN": "token1",
		"DISCORD_GUILD_ID":  "guild1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if config.Mode != ModeDaemon {
		t.Fatalf("Mode = %q, want %q", config.Mode, ModeDaemon)
	}
	if !config.Schedule.RunOnStart {
		t.Fatal("Schedule.RunOnStart = false, want true")
	}
	if config.Schedule.Time != defaultScheduleTime {
		t.Fatalf("Schedule.Time = %q, want %q", config.Schedule.Time, defaultScheduleTime)
	}
	if config.Schedule.Timezone != archive.DefaultLocation {
		t.Fatalf("Schedule.Timezone = %q, want %q", config.Schedule.Timezone, archive.DefaultLocation)
	}
	if config.Archive.Token != "token1" || config.Archive.GuildID != "guild1" {
		t.Fatalf("archive credentials not resolved: %#v", config.Archive)
	}
}

func TestParseDaemonFlagsOverrideEnv(t *testing.T) {
	config, err := Parse([]string{
		"daemon",
		"-schedule-time", "04:30",
		"-timezone", "UTC",
		"-run-on-start=true",
		"-no-private-threads",
	}, testEnv(map[string]string{
		"DISCORD_ARCHIVER_SCHEDULE_TIME": "03:00",
		"DISCORD_ARCHIVER_RUN_ON_START":  "false",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if config.Schedule.Time != "04:30" {
		t.Fatalf("Schedule.Time = %q, want %q", config.Schedule.Time, "04:30")
	}
	if config.Schedule.Timezone != "UTC" {
		t.Fatalf("Schedule.Timezone = %q, want %q", config.Schedule.Timezone, "UTC")
	}
	if !config.Schedule.RunOnStart {
		t.Fatal("Schedule.RunOnStart = false, want true")
	}
	if config.Archive.IncludePrivate {
		t.Fatal("Archive.IncludePrivate = true, want false")
	}
}

func TestParseNoRunOnStart(t *testing.T) {
	config, err := Parse([]string{"-no-run-on-start"}, testEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if config.Schedule.RunOnStart {
		t.Fatal("Schedule.RunOnStart = true, want false")
	}
}

func TestParseDumpAll(t *testing.T) {
	config, err := Parse([]string{"dump", "-all"}, testEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if config.Mode != ModeDump {
		t.Fatalf("Mode = %q, want %q", config.Mode, ModeDump)
	}
	if config.Date != "" {
		t.Fatalf("Date = %q, want empty", config.Date)
	}
}

func TestParseDumpDate(t *testing.T) {
	config, err := Parse([]string{"dump", "-date", "2026-07-09"}, testEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if config.Mode != ModeDump {
		t.Fatalf("Mode = %q, want %q", config.Mode, ModeDump)
	}
	if config.Date != "2026-07-09" {
		t.Fatalf("Date = %q, want %q", config.Date, "2026-07-09")
	}
}

func TestParseDumpRequiresAllOrDate(t *testing.T) {
	if _, err := Parse([]string{"dump"}, testEnv(nil)); err == nil {
		t.Fatal("Parse dump without target succeeded, want error")
	}
	if _, err := Parse([]string{"dump", "-all", "-date", "2026-07-09"}, testEnv(nil)); err == nil {
		t.Fatal("Parse dump with all and date succeeded, want error")
	}
}

func testEnv(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}
