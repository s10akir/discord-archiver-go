package cli

import (
	"path/filepath"
	"testing"
)

func TestParseStateFileDefaultsInsideArchive(t *testing.T) {
	config, err := Parse([]string{"-archive-dir", "/archive"}, func(key string) string {
		if key == "DISCORD_ARCHIVE_WEB_URL" {
			return "http://web"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/archive", ".discord-archive-importer-state.json")
	if config.StateFile != want {
		t.Fatalf("StateFile = %q, want %q", config.StateFile, want)
	}
}

func TestParseExplicitStateFile(t *testing.T) {
	config, err := Parse([]string{"-state-file", "/state/importer.json"}, func(string) string { return "http://web" })
	if err != nil {
		t.Fatal(err)
	}
	if config.StateFile != "/state/importer.json" {
		t.Fatalf("StateFile = %q", config.StateFile)
	}
}
