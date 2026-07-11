package cli

import "testing"

func TestParseDefaults(t *testing.T) {
	config, err := Parse(nil, func(key string) string {
		if key == "DATABASE_URL" {
			return "postgres://localhost/archive"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.ArchiveDir != "archive" || config.Addr != ":8080" {
		t.Fatalf("config = %#v", config)
	}
}

func TestParseEnvironmentAndFlagPriority(t *testing.T) {
	config, err := Parse([]string{"-out-dir", "data", "-addr", ":9090"}, func(key string) string {
		if key == "DATABASE_URL" {
			return "postgres://localhost/archive"
		}
		if key == "DISCORD_ARCHIVE_WEB_ADDR" {
			return ":8081"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.ArchiveDir != "data" || config.Addr != ":9090" {
		t.Fatalf("config = %#v", config)
	}
}

func TestParseRejectsUnexpectedArgument(t *testing.T) {
	if _, err := Parse([]string{"extra"}, func(string) string { return "" }); err == nil {
		t.Fatal("Parse succeeded, want error")
	}
}

func TestParseRequiresDatabaseURL(t *testing.T) {
	if _, err := Parse(nil, func(string) string { return "" }); err == nil {
		t.Fatal("Parse succeeded, want error")
	}
}
