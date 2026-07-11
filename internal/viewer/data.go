// Package viewer serves a read-only browser UI over an archive directory
// produced by the archive package, without needing a Discord token.
package viewer

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/s10akir/discord-archiver-go/internal/archive"
)

// channelMetaLine and threadMetaLine mirror the JSON shape written by
// archive.archiveOutput (channelRecord/threadRecord); the viewer only reads
// the archive, so it decodes the format directly rather than importing
// unexported types.
type channelMetaLine struct {
	Channel *discordgo.Channel `json:"channel"`
}

type threadMetaLine struct {
	Source string             `json:"source"`
	Thread *discordgo.Channel `json:"thread"`
}

type messageLine struct {
	ChannelID   string             `json:"channel_id"`
	ChannelName string             `json:"channel_name"`
	ParentID    string             `json:"parent_id"`
	Message     *discordgo.Message `json:"message"`
}

type archivedMessage struct {
	Date    string
	Message *discordgo.Message
}

type messageCursor struct {
	Date      string `json:"date"`
	Timestamp string `json:"timestamp"`
	ID        string `json:"id"`
}

type messagePage struct {
	Messages   []archivedMessage
	NextCursor *messageCursor
	HasMore    bool
}

type messageStore interface {
	Page(channelID string, before *messageCursor, limit int) (messagePage, error)
	MediaPage(channelID string, before *messageCursor, limit int) (messagePage, error)
}

type jsonlMessageStore struct {
	root string
}

func (s jsonlMessageStore) Page(channelID string, before *messageCursor, limit int) (messagePage, error) {
	page, err := s.page(channelID, before, limit, nil)
	if err != nil {
		return messagePage{}, err
	}
	// The message view reads chronologically from top to bottom.
	for i, j := 0, len(page.Messages)-1; i < j; i, j = i+1, j-1 {
		page.Messages[i], page.Messages[j] = page.Messages[j], page.Messages[i]
	}
	return page, nil
}

func (s jsonlMessageStore) MediaPage(channelID string, before *messageCursor, limit int) (messagePage, error) {
	return s.page(channelID, before, limit, func(message *discordgo.Message) bool {
		return len(message.Attachments) > 0 || len(message.Embeds) > 0
	})
}

func (s jsonlMessageStore) page(channelID string, before *messageCursor, limit int, include func(*discordgo.Message) bool) (messagePage, error) {
	if limit <= 0 {
		return messagePage{}, nil
	}
	dates, err := listDates(s.root, channelID)
	if err != nil {
		return messagePage{}, err
	}

	var cursorTime time.Time
	if before != nil {
		cursorTime, err = time.Parse(time.RFC3339Nano, before.Timestamp)
		if err != nil {
			return messagePage{}, fmt.Errorf("parse cursor timestamp: %w", err)
		}
	}

	// Collect newest-to-oldest. One extra record tells the caller whether
	// another page exists without reading every older date partition.
	collected := make([]archivedMessage, 0, limit+1)
	for i := len(dates) - 1; i >= 0 && len(collected) <= limit; i-- {
		date := dates[i]
		if before != nil && date > before.Date {
			continue
		}
		messages, err := loadMessages(s.root, date, channelID)
		if err != nil {
			return messagePage{}, err
		}
		for j := len(messages) - 1; j >= 0 && len(collected) <= limit; j-- {
			message := messages[j]
			if before != nil && date == before.Date && !messageBefore(message, cursorTime, before.ID) {
				continue
			}
			if include != nil && !include(message) {
				continue
			}
			collected = append(collected, archivedMessage{Date: date, Message: message})
		}
	}

	hasMore := len(collected) > limit
	if hasMore {
		collected = collected[:limit]
	}
	page := messagePage{Messages: collected, HasMore: hasMore}
	if hasMore && len(collected) > 0 {
		oldest := collected[len(collected)-1]
		page.NextCursor = &messageCursor{
			Date:      oldest.Date,
			Timestamp: oldest.Message.Timestamp.Format(time.RFC3339Nano),
			ID:        oldest.Message.ID,
		}
	}
	return page, nil
}

func messageBefore(message *discordgo.Message, timestamp time.Time, id string) bool {
	if message.Timestamp.Before(timestamp) {
		return true
	}
	return message.Timestamp.Equal(timestamp) && message.ID < id
}

func encodeCursor(cursor *messageCursor) string {
	if cursor == nil {
		return ""
	}
	data, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(value string) (*messageCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode cursor: %w", err)
	}
	var cursor messageCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return nil, fmt.Errorf("unmarshal cursor: %w", err)
	}
	if !dateDirPattern.MatchString("date="+cursor.Date) || cursor.ID == "" {
		return nil, fmt.Errorf("invalid cursor")
	}
	if _, err := time.Parse(time.RFC3339Nano, cursor.Timestamp); err != nil {
		return nil, fmt.Errorf("invalid cursor timestamp: %w", err)
	}
	return &cursor, nil
}

// container is a message-bearing entity: either a regular channel or a
// thread. Both are stored under messages/date=*/channel_id=<ID>/ the same
// way, so the viewer treats them uniformly once loaded.
type container struct {
	ID       string
	Name     string
	Type     discordgo.ChannelType
	ParentID string
	IsThread bool
	Source   string // thread archive source; empty for regular channels
}

var guildDirPattern = regexp.MustCompile(`^guild_id=(.+)$`)

// listGuilds returns guild IDs found directly under archiveDir.
func listGuilds(archiveDir string) ([]string, error) {
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		return nil, fmt.Errorf("read archive directory: %w", err)
	}

	var guilds []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if match := guildDirPattern.FindStringSubmatch(entry.Name()); match != nil {
			guilds = append(guilds, match[1])
		}
	}
	sort.Strings(guilds)
	return guilds, nil
}

func guildRoot(archiveDir, guildID string) string {
	return filepath.Join(archiveDir, "guild_id="+guildID)
}

// loadContainers reads metadata/channels.jsonl and metadata/threads.jsonl.
// It returns every message-bearing channel and thread (containers), plus a
// name lookup covering all channels including ones that cannot hold messages
// directly (categories, forums) so those can still be used as group headers
// and to resolve <#id> mentions. Missing metadata files are treated as empty
// rather than an error, since a partial archive may not have committed them
// yet.
func loadContainers(root string) (containers []container, names map[string]string, err error) {
	names = make(map[string]string)

	channels := make(map[string]*discordgo.Channel)
	if err := decodeJSONLines(filepath.Join(root, "metadata", "channels.jsonl"), func(line channelMetaLine) {
		if line.Channel != nil {
			channels[line.Channel.ID] = line.Channel
		}
	}); err != nil {
		return nil, nil, err
	}
	for _, channel := range channels {
		names[channel.ID] = channel.Name
		if !archive.CanContainMessages(channel.Type) {
			continue
		}
		containers = append(containers, container{
			ID:       channel.ID,
			Name:     channel.Name,
			Type:     channel.Type,
			ParentID: channel.ParentID,
		})
	}

	threads := make(map[string]threadMetaLine)
	if err := decodeJSONLines(filepath.Join(root, "metadata", "threads.jsonl"), func(line threadMetaLine) {
		if line.Thread != nil {
			threads[line.Thread.ID] = line
		}
	}); err != nil {
		return nil, nil, err
	}
	for _, line := range threads {
		names[line.Thread.ID] = line.Thread.Name
		containers = append(containers, container{
			ID:       line.Thread.ID,
			Name:     line.Thread.Name,
			Type:     line.Thread.Type,
			ParentID: line.Thread.ParentID,
			IsThread: true,
			Source:   line.Source,
		})
	}

	sort.Slice(containers, func(i, j int) bool {
		if containers[i].ParentID != containers[j].ParentID {
			return containers[i].ParentID < containers[j].ParentID
		}
		if containers[i].IsThread != containers[j].IsThread {
			return !containers[i].IsThread
		}
		return containers[i].Name < containers[j].Name
	})
	return containers, names, nil
}

func findContainer(containers []container, id string) (container, bool) {
	for _, c := range containers {
		if c.ID == id {
			return c, true
		}
	}
	return container{}, false
}

var dateDirPattern = regexp.MustCompile(`^date=(\d{4}-\d{2}-\d{2})$`)

// listDates returns, in ascending order, every date for which channelID has
// an archived messages.jsonl file.
func listDates(root, channelID string) ([]string, error) {
	messagesRoot := filepath.Join(root, "messages")
	entries, err := os.ReadDir(messagesRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read messages directory: %w", err)
	}

	var dates []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		match := dateDirPattern.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		path := filepath.Join(messagesRoot, entry.Name(), "channel_id="+channelID, "messages.jsonl")
		if _, err := os.Stat(path); err == nil {
			dates = append(dates, match[1])
		}
	}
	sort.Strings(dates)
	return dates, nil
}

// loadMessages reads and sorts (ascending by timestamp) every message
// archived for channelID on date.
func loadMessages(root, date, channelID string) ([]*discordgo.Message, error) {
	path := filepath.Join(root, "messages", "date="+date, "channel_id="+channelID, "messages.jsonl")

	var messages []*discordgo.Message
	err := decodeJSONLines(path, func(line messageLine) {
		if line.Message != nil {
			messages = append(messages, line.Message)
		}
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(messages, func(i, j int) bool {
		if messages[i].Timestamp.Equal(messages[j].Timestamp) {
			return messages[i].ID < messages[j].ID
		}
		return messages[i].Timestamp.Before(messages[j].Timestamp)
	})
	return messages, nil
}

// attachmentURL builds the path (under the /files/ route) an attachment is
// served at.
func attachmentURL(guildID, date, channelID, messageID string, attachment *discordgo.MessageAttachment) string {
	relPath := archive.AttachmentRelPath(date, channelID, messageID, attachment)
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return "/files/" + url.PathEscape(guildID) + "/" + strings.Join(parts, "/")
}

// hasLocalAttachment reports whether an attachment for (date, channelID,
// messageID) was actually downloaded into the archive. Attachments archived
// before the archiver started downloading files (or that failed to download)
// have metadata only, with no file on disk.
func hasLocalAttachment(root, date, channelID, messageID string, attachment *discordgo.MessageAttachment) bool {
	relPath := archive.AttachmentRelPath(date, channelID, messageID, attachment)
	_, err := os.Stat(filepath.Join(root, relPath))
	return err == nil
}

func decodeJSONLines[T any](path string, fn func(T)) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var value T
		if err := json.Unmarshal(line, &value); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
		fn(value)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}
