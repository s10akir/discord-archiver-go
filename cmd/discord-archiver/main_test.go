package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestPartitionDateUsesJST(t *testing.T) {
	loc, err := time.LoadLocation(jstLocation)
	if err != nil {
		t.Fatal(err)
	}

	messageTime := time.Date(2026, 7, 8, 15, 0, 0, 0, time.UTC)
	if got := partitionDate(messageTime, loc); got != "2026-07-09" {
		t.Fatalf("partitionDate() = %q, want %q", got, "2026-07-09")
	}
}

func TestParseDateFilterUsesJSTDay(t *testing.T) {
	loc, err := time.LoadLocation(jstLocation)
	if err != nil {
		t.Fatal(err)
	}

	filter, err := parseDateFilter("2026-07-09", loc)
	if err != nil {
		t.Fatal(err)
	}

	if !filter.Start.Equal(time.Date(2026, 7, 9, 0, 0, 0, 0, loc)) {
		t.Fatalf("Start = %s", filter.Start)
	}
	if !filter.End.Equal(time.Date(2026, 7, 10, 0, 0, 0, 0, loc)) {
		t.Fatalf("End = %s", filter.End)
	}
}

func TestParseCommandDefaultsToDaemon(t *testing.T) {
	config, err := parseCommand(nil, testEnv(map[string]string{
		"DISCORD_BOT_TOKEN": "token1",
		"DISCORD_GUILD_ID":  "guild1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if config.mode != commandDaemon {
		t.Fatalf("mode = %q, want %q", config.mode, commandDaemon)
	}
	if !config.runOnStart {
		t.Fatal("runOnStart = false, want true")
	}
	if config.scheduleTime != defaultScheduleTime {
		t.Fatalf("scheduleTime = %q, want %q", config.scheduleTime, defaultScheduleTime)
	}
	if config.timezone != jstLocation {
		t.Fatalf("timezone = %q, want %q", config.timezone, jstLocation)
	}
	if config.archive.token != "token1" || config.archive.guildID != "guild1" {
		t.Fatalf("archive credentials not resolved: %#v", config.archive)
	}
}

func TestParseCommandDaemonFlagsOverrideEnv(t *testing.T) {
	config, err := parseCommand([]string{
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
	if config.scheduleTime != "04:30" {
		t.Fatalf("scheduleTime = %q, want %q", config.scheduleTime, "04:30")
	}
	if config.timezone != "UTC" {
		t.Fatalf("timezone = %q, want %q", config.timezone, "UTC")
	}
	if !config.runOnStart {
		t.Fatal("runOnStart = false, want true")
	}
	if config.archive.includePrivate {
		t.Fatal("includePrivate = true, want false")
	}
}

func TestParseCommandNoRunOnStart(t *testing.T) {
	config, err := parseCommand([]string{"-no-run-on-start"}, testEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if config.runOnStart {
		t.Fatal("runOnStart = true, want false")
	}
}

func TestParseCommandDumpAll(t *testing.T) {
	config, err := parseCommand([]string{"dump", "-all"}, testEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if config.mode != commandDump {
		t.Fatalf("mode = %q, want %q", config.mode, commandDump)
	}
	if config.date != "" {
		t.Fatalf("date = %q, want empty", config.date)
	}
}

func TestParseCommandDumpDate(t *testing.T) {
	config, err := parseCommand([]string{"dump", "-date", "2026-07-09"}, testEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if config.mode != commandDump {
		t.Fatalf("mode = %q, want %q", config.mode, commandDump)
	}
	if config.date != "2026-07-09" {
		t.Fatalf("date = %q, want %q", config.date, "2026-07-09")
	}
}

func TestParseCommandDumpRequiresAllOrDate(t *testing.T) {
	if _, err := parseCommand([]string{"dump"}, testEnv(nil)); err == nil {
		t.Fatal("parseCommand dump without target succeeded, want error")
	}
	if _, err := parseCommand([]string{"dump", "-all", "-date", "2026-07-09"}, testEnv(nil)); err == nil {
		t.Fatal("parseCommand dump with all and date succeeded, want error")
	}
}

func TestParseScheduleClock(t *testing.T) {
	clock, err := parseScheduleClock("03:05")
	if err != nil {
		t.Fatal(err)
	}
	if clock.hour != 3 || clock.minute != 5 {
		t.Fatalf("clock = %#v, want 03:05", clock)
	}

	for _, value := range []string{"", "3", "24:00", "03:60", "aa:00"} {
		if _, err := parseScheduleClock(value); err == nil {
			t.Fatalf("parseScheduleClock(%q) succeeded, want error", value)
		}
	}
}

func TestNextScheduledTime(t *testing.T) {
	loc, err := time.LoadLocation(jstLocation)
	if err != nil {
		t.Fatal(err)
	}
	schedule := scheduleClock{hour: 3}

	now := time.Date(2026, 7, 9, 2, 0, 0, 0, loc)
	want := time.Date(2026, 7, 9, 3, 0, 0, 0, loc)
	if got := nextScheduledTime(now, schedule, loc); !got.Equal(want) {
		t.Fatalf("nextScheduledTime before schedule = %s, want %s", got, want)
	}

	now = time.Date(2026, 7, 9, 3, 0, 0, 0, loc)
	want = time.Date(2026, 7, 10, 3, 0, 0, 0, loc)
	if got := nextScheduledTime(now, schedule, loc); !got.Equal(want) {
		t.Fatalf("nextScheduledTime at schedule = %s, want %s", got, want)
	}
}

func TestPreviousDateUsesScheduleTimezone(t *testing.T) {
	loc, err := time.LoadLocation(jstLocation)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 9, 0, 30, 0, 0, loc)
	if got := previousDate(now, loc); got != "2026-07-08" {
		t.Fatalf("previousDate() = %q, want %q", got, "2026-07-08")
	}
}

func testEnv(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}

func TestChannelMessagesCompatDecodeIgnoresUnknownComponentTypes(t *testing.T) {
	body := []byte(`[
		{
			"id":"112233445566778899",
			"channel_id":"channel1",
			"content":"hello",
			"timestamp":"2026-07-09T01:02:03.000000+00:00",
			"edited_timestamp":null,
			"tts":false,
			"mention_everyone":false,
			"mentions":[],
			"mention_roles":[],
			"attachments":[],
			"embeds":[],
			"pinned":false,
			"type":0,
			"components":[{"type":20,"unknown":"value"}],
			"author":{"id":"user1","username":"user","discriminator":"0001","avatar":null,"bot":false}
		}
	]`)

	messages, err := unmarshalMessagesWithoutComponents(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1", len(messages))
	}
	if messages[0].Content != "hello" {
		t.Fatalf("Content = %q", messages[0].Content)
	}
}

func TestArchiveOutputWritesByDateAndChannel(t *testing.T) {
	root := t.TempDir()
	output, err := newArchiveOutput(root, "guild1", nil)
	if err != nil {
		t.Fatal(err)
	}

	record := archiveRecord{
		GuildID:     "guild1",
		ChannelID:   "channel1",
		ChannelName: "general",
		ChannelType: discordgo.ChannelTypeGuildText,
		Message: &discordgo.Message{
			ID:        "message1",
			ChannelID: "channel1",
			Content:   "hello",
			Timestamp: time.Date(2026, 7, 8, 15, 0, 0, 0, time.UTC),
		},
	}
	if err := output.WriteMessage("2026-07-09", "channel1", record); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(root, "guild_id=guild1", "messages", "date=2026-07-09", "channel_id=channel1", "messages.jsonl")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	var got archiveRecord
	if err := json.NewDecoder(file).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Message.Content != "hello" {
		t.Fatalf("message content = %q", got.Message.Content)
	}
}

func TestArchiveOutputDateCommitReplacesTarget(t *testing.T) {
	root := t.TempDir()
	loc, err := time.LoadLocation(jstLocation)
	if err != nil {
		t.Fatal(err)
	}
	filter, err := parseDateFilter("2026-07-09", loc)
	if err != nil {
		t.Fatal(err)
	}

	existing := filepath.Join(root, "guild_id=guild1", "messages", "date=2026-07-09", "channel_id=old", "messages.jsonl")
	if err := os.MkdirAll(filepath.Dir(existing), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	output, err := newArchiveOutput(root, "guild1", filter)
	if err != nil {
		t.Fatal(err)
	}
	if err := output.WriteMessage("2026-07-09", "new", archiveRecord{
		GuildID:   "guild1",
		ChannelID: "new",
		Message: &discordgo.Message{
			ID:        "message1",
			ChannelID: "new",
			Content:   "new",
			Timestamp: time.Date(2026, 7, 8, 15, 0, 0, 0, time.UTC),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if err := output.Commit(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(existing); !os.IsNotExist(err) {
		t.Fatalf("old date file still exists: %v", err)
	}
	newPath := filepath.Join(root, "guild_id=guild1", "messages", "date=2026-07-09", "channel_id=new", "messages.jsonl")
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new date file missing: %v", err)
	}
}
