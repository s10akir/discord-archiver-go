package archive

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestPartitionDateUsesJST(t *testing.T) {
	loc, err := time.LoadLocation(DefaultLocation)
	if err != nil {
		t.Fatal(err)
	}

	messageTime := time.Date(2026, 7, 8, 15, 0, 0, 0, time.UTC)
	if got := partitionDate(messageTime, loc); got != "2026-07-09" {
		t.Fatalf("partitionDate() = %q, want %q", got, "2026-07-09")
	}
}

func TestParseDateFilterUsesJSTDay(t *testing.T) {
	loc, err := time.LoadLocation(DefaultLocation)
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
	loc, err := time.LoadLocation(DefaultLocation)
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

func TestArchiveOutputMetadataReplacedOnCommit(t *testing.T) {
	root := t.TempDir()
	metadataPath := filepath.Join(root, "guild_id=guild1", "metadata", "channels.jsonl")
	if err := os.MkdirAll(filepath.Dir(metadataPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metadataPath, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	output, err := newArchiveOutput(root, "guild1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := output.WriteChannelMetadata(channelRecord{
		GuildID: "guild1",
		Channel: &discordgo.Channel{ID: "channel1", Name: "general"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}

	// Before Commit the previous metadata must be untouched.
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old\n" {
		t.Fatalf("metadata overwritten before Commit: %q", data)
	}

	if err := output.Commit(); err != nil {
		t.Fatal(err)
	}

	file, err := os.Open(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var got channelRecord
	if err := json.NewDecoder(file).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Channel.ID != "channel1" {
		t.Fatalf("channel id = %q", got.Channel.ID)
	}
}

func TestArchiveOutputCleanupWithoutCommitKeepsOldMetadata(t *testing.T) {
	root := t.TempDir()
	metadataPath := filepath.Join(root, "guild_id=guild1", "metadata", "channels.jsonl")
	if err := os.MkdirAll(filepath.Dir(metadataPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metadataPath, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	output, err := newArchiveOutput(root, "guild1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := output.WriteChannelMetadata(channelRecord{
		GuildID: "guild1",
		Channel: &discordgo.Channel{ID: "channel1"},
	}); err != nil {
		t.Fatal(err)
	}
	output.Cleanup()

	data, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old\n" {
		t.Fatalf("metadata overwritten without Commit: %q", data)
	}

	entries, err := os.ReadDir(filepath.Dir(metadataPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "channels.jsonl" {
			t.Fatalf("leftover metadata temp file: %s", entry.Name())
		}
	}
}

func TestNewArchiveOutputRemovesStaleTempEntries(t *testing.T) {
	root := t.TempDir()
	messagesRoot := filepath.Join(root, "guild_id=guild1", "messages")
	metadataRoot := filepath.Join(root, "guild_id=guild1", "metadata")

	staleNano := time.Now().Add(-48 * time.Hour).UnixNano()
	freshNano := time.Now().UnixNano()
	staleTempDir := filepath.Join(messagesRoot, fmt.Sprintf(".date=2026-07-01.tmp-123-%d", staleNano))
	staleBackupDir := filepath.Join(messagesRoot, fmt.Sprintf(".date=2026-07-01.backup-123-%d", staleNano))
	freshTempDir := filepath.Join(messagesRoot, fmt.Sprintf(".date=2026-07-01.tmp-456-%d", freshNano))
	realDateDir := filepath.Join(messagesRoot, "date=2026-07-01")
	for _, dir := range []string{staleTempDir, staleBackupDir, freshTempDir, realDateDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	staleMetadataFile := filepath.Join(metadataRoot, fmt.Sprintf(".channels.jsonl.tmp-123-%d", staleNano))
	if err := os.MkdirAll(metadataRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staleMetadataFile, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := newArchiveOutput(root, "guild1", nil); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{staleTempDir, staleBackupDir, staleMetadataFile} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("stale entry still exists: %s", path)
		}
	}
	for _, path := range []string{freshTempDir, realDateDir} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("non-stale entry removed: %s: %v", path, err)
		}
	}
}

type fakeDiscordClient struct {
	channels       []*discordgo.Channel
	messages       map[string][]*discordgo.Message
	failChannels   map[string]error
	attachmentData map[string][]byte
	attachmentErrs map[string]error

	mu            sync.Mutex
	downloadCalls []string
	messageCalls  map[string]int
}

func (c *fakeDiscordClient) GuildChannels(guildID string) ([]*discordgo.Channel, error) {
	return c.channels, nil
}

func (c *fakeDiscordClient) GuildThreadsActive(guildID string) (*discordgo.ThreadsList, error) {
	return &discordgo.ThreadsList{}, nil
}

func (c *fakeDiscordClient) ThreadsArchived(channelID string, before *time.Time, limit int) (*discordgo.ThreadsList, error) {
	return &discordgo.ThreadsList{}, nil
}

func (c *fakeDiscordClient) ThreadsPrivateArchived(channelID string, before *time.Time, limit int) (*discordgo.ThreadsList, error) {
	return &discordgo.ThreadsList{}, nil
}

// ChannelMessages returns the channel's fixed message set on its first call
// and an empty page thereafter, regardless of beforeID. Real pagination
// depends on beforeID reflecting message order, which these fakes' string
// IDs don't; tests that need multi-page behavior fake ChannelMessages
// themselves.
func (c *fakeDiscordClient) ChannelMessages(channelID string, limit int, beforeID string) ([]*discordgo.Message, error) {
	if err := c.failChannels[channelID]; err != nil {
		return nil, err
	}

	c.mu.Lock()
	if c.messageCalls == nil {
		c.messageCalls = make(map[string]int)
	}
	call := c.messageCalls[channelID]
	c.messageCalls[channelID]++
	c.mu.Unlock()

	if call > 0 {
		return nil, nil
	}
	return c.messages[channelID], nil
}

func (c *fakeDiscordClient) DownloadAttachment(url string) (io.ReadCloser, error) {
	c.mu.Lock()
	c.downloadCalls = append(c.downloadCalls, url)
	c.mu.Unlock()

	if err := c.attachmentErrs[url]; err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(c.attachmentData[url])), nil
}

func (c *fakeDiscordClient) Close() error { return nil }

func TestRunArchiveCollectsChannelFailures(t *testing.T) {
	root := t.TempDir()
	loc, err := time.LoadLocation(DefaultLocation)
	if err != nil {
		t.Fatal(err)
	}

	client := &fakeDiscordClient{
		channels: []*discordgo.Channel{
			{ID: "bad", Name: "bad", Type: discordgo.ChannelTypeGuildText},
			{ID: "good", Name: "good", Type: discordgo.ChannelTypeGuildText},
		},
		messages: map[string][]*discordgo.Message{
			"good": {{
				ID:        "message1",
				ChannelID: "good",
				Content:   "hello",
				Timestamp: time.Date(2026, 7, 8, 15, 0, 0, 0, time.UTC),
			}},
		},
		failChannels: map[string]error{"bad": errors.New("boom")},
	}

	output, err := newArchiveOutput(root, "guild1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Cleanup()

	config := Config{Token: "token", GuildID: "guild1", OutputDir: root, IncludeThreads: true}
	err = runArchive(client, output, config, nil, loc)
	if err == nil {
		t.Fatal("runArchive() = nil, want error for failed channel")
	}
	if !strings.Contains(err.Error(), "bad") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error missing failed channel context: %v", err)
	}

	// The failure on one channel must not prevent archiving the others.
	messagePath := filepath.Join(root, "guild_id=guild1", "messages", "date=2026-07-09", "channel_id=good", "messages.jsonl")
	if _, err := os.Stat(messagePath); err != nil {
		t.Fatalf("good channel messages missing: %v", err)
	}
	metadataPath := filepath.Join(root, "guild_id=guild1", "metadata", "channels.jsonl")
	if _, err := os.Stat(metadataPath); err != nil {
		t.Fatalf("channel metadata not committed: %v", err)
	}
}

func TestRunArchiveDownloadsAttachments(t *testing.T) {
	root := t.TempDir()
	loc, err := time.LoadLocation(DefaultLocation)
	if err != nil {
		t.Fatal(err)
	}

	client := &fakeDiscordClient{
		channels: []*discordgo.Channel{{ID: "chan", Name: "general", Type: discordgo.ChannelTypeGuildText}},
		messages: map[string][]*discordgo.Message{
			"chan": {{
				ID:        "message1",
				ChannelID: "chan",
				Timestamp: time.Date(2026, 7, 8, 15, 0, 0, 0, time.UTC),
				Attachments: []*discordgo.MessageAttachment{
					{ID: "att1", Filename: "photo.png", Size: 5, URL: "https://cdn.example/att1"},
				},
			}},
		},
		attachmentData: map[string][]byte{"https://cdn.example/att1": []byte("hello")},
	}

	output, err := newArchiveOutput(root, "guild1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Cleanup()

	config := Config{Token: "token", GuildID: "guild1", OutputDir: root, DownloadAttachments: true}
	if err := runArchive(client, output, config, nil, loc); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(root, "guild_id=guild1", "messages", "date=2026-07-09", "channel_id=chan", "attachments", "message1", "att1-photo.png")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("attachment not written: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("attachment content = %q, want %q", data, "hello")
	}
}

func TestRunArchiveSkipsUpToDateAttachmentOnDateRerun(t *testing.T) {
	root := t.TempDir()
	loc, err := time.LoadLocation(DefaultLocation)
	if err != nil {
		t.Fatal(err)
	}
	filter, err := parseDateFilter("2026-07-09", loc)
	if err != nil {
		t.Fatal(err)
	}

	existing := filepath.Join(root, "guild_id=guild1", "messages", "date=2026-07-09", "channel_id=chan", "attachments", "message1", "att1-photo.png")
	if err := os.MkdirAll(filepath.Dir(existing), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	client := &fakeDiscordClient{
		channels: []*discordgo.Channel{{ID: "chan", Name: "general", Type: discordgo.ChannelTypeGuildText}},
		messages: map[string][]*discordgo.Message{
			"chan": {{
				ID:        "message1",
				ChannelID: "chan",
				Timestamp: time.Date(2026, 7, 9, 1, 0, 0, 0, time.UTC),
				Attachments: []*discordgo.MessageAttachment{
					{ID: "att1", Filename: "photo.png", Size: 5, URL: "https://cdn.example/att1"},
				},
			}},
		},
		attachmentErrs: map[string]error{"https://cdn.example/att1": errors.New("should not be called")},
	}

	output, err := newArchiveOutput(root, "guild1", filter)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Cleanup()

	config := Config{Token: "token", GuildID: "guild1", OutputDir: root, DownloadAttachments: true}
	if err := runArchive(client, output, config, filter, loc); err != nil {
		t.Fatal(err)
	}

	if len(client.downloadCalls) != 0 {
		t.Fatalf("DownloadAttachment called %d times, want 0", len(client.downloadCalls))
	}
	data, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("attachment missing after rerun: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("attachment content = %q, want %q", data, "hello")
	}
}

func TestRunArchiveAttachmentDownloadFailureIsSoftFailure(t *testing.T) {
	root := t.TempDir()
	loc, err := time.LoadLocation(DefaultLocation)
	if err != nil {
		t.Fatal(err)
	}

	client := &fakeDiscordClient{
		channels: []*discordgo.Channel{{ID: "chan", Name: "general", Type: discordgo.ChannelTypeGuildText}},
		messages: map[string][]*discordgo.Message{
			"chan": {{
				ID:        "message1",
				ChannelID: "chan",
				Content:   "hello",
				Timestamp: time.Date(2026, 7, 8, 15, 0, 0, 0, time.UTC),
				Attachments: []*discordgo.MessageAttachment{
					{ID: "att1", Filename: "photo.png", Size: 5, URL: "https://cdn.example/att1"},
				},
			}},
		},
		attachmentErrs: map[string]error{"https://cdn.example/att1": errors.New("boom")},
	}

	output, err := newArchiveOutput(root, "guild1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Cleanup()

	config := Config{Token: "token", GuildID: "guild1", OutputDir: root, DownloadAttachments: true}
	err = runArchive(client, output, config, nil, loc)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("runArchive() error = %v, want error containing %q", err, "boom")
	}

	messagePath := filepath.Join(root, "guild_id=guild1", "messages", "date=2026-07-09", "channel_id=chan", "messages.jsonl")
	data, err := os.ReadFile(messagePath)
	if err != nil {
		t.Fatalf("message not written despite attachment failure: %v", err)
	}
	if !strings.Contains(string(data), "hello") {
		t.Fatalf("message content missing: %q", data)
	}
}
