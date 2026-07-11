package archiveformat

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
)

func TestAttachmentRelPath(t *testing.T) {
	attachment := &discordgo.MessageAttachment{ID: "att1", Filename: "photo.png"}
	got := AttachmentRelPath("2026-07-09", "channel1", "message1", attachment)
	want := filepath.Join("messages", "date=2026-07-09", "channel_id=channel1", "attachments", "message1", "att1-photo.png")
	if got != want {
		t.Fatalf("AttachmentRelPath() = %q, want %q", got, want)
	}
}

func TestAttachmentRelPathSanitizesAndTruncatesFilename(t *testing.T) {
	attachment := &discordgo.MessageAttachment{ID: "att1", Filename: "../" + strings.Repeat("あ", 100) + ".png"}
	got := AttachmentRelPath("2026-07-09", "channel1", "message1", attachment)
	name := filepath.Base(got)
	if len(name) > 255 {
		t.Fatalf("filename has %d bytes, want at most 255", len(name))
	}
	if !utf8.ValidString(name) {
		t.Fatalf("filename is not valid UTF-8: %q", name)
	}
	if filepath.Ext(name) != ".png" {
		t.Fatalf("extension = %q, want .png", filepath.Ext(name))
	}
}

func TestCanContainMessages(t *testing.T) {
	if !CanContainMessages(discordgo.ChannelTypeGuildText) {
		t.Fatal("text channel cannot contain messages")
	}
	if CanContainMessages(discordgo.ChannelTypeGuildForum) {
		t.Fatal("forum channel can contain messages")
	}
}
