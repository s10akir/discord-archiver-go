// Package web serves the archive browser web application.
// without needing a Discord token.
package web

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/s10akir/discord-archiver-go/pkg/archiveformat"
)

type channelMetaLine = archiveformat.ChannelRecord
type threadMetaLine = archiveformat.ThreadRecord
type messageLine = archiveformat.MessageRecord

type archivedMessage struct {
	GuildID     string
	Date        string
	ChannelID   string
	ChannelName string
	Message     *discordgo.Message
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
	MediaPage(channelID string, kind mediaKind, before *messageCursor, limit int) (messagePage, error)
	AllPage(before *messageCursor, limit int) (messagePage, error)
	AllMediaPage(kind mediaKind, before *messageCursor, limit int) (messagePage, error)
}

type mediaKind string

const (
	mediaImage mediaKind = "images"
	mediaVideo mediaKind = "videos"
	mediaAudio mediaKind = "audio"
	mediaFile  mediaKind = "files"
	mediaEmbed mediaKind = "embeds"
)

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

func (s jsonlMessageStore) MediaPage(channelID string, kind mediaKind, before *messageCursor, limit int) (messagePage, error) {
	return s.page(channelID, before, limit, func(message *discordgo.Message) bool {
		if kind == mediaEmbed {
			return len(message.Embeds) > 0
		}
		for _, attachment := range message.Attachments {
			if attachmentMediaKind(attachment) == kind {
				return true
			}
		}
		return false
	})
}

func (s jsonlMessageStore) AllPage(before *messageCursor, limit int) (messagePage, error) {
	page, err := s.allPage(before, limit, nil)
	if err != nil {
		return messagePage{}, err
	}
	for i, j := 0, len(page.Messages)-1; i < j; i, j = i+1, j-1 {
		page.Messages[i], page.Messages[j] = page.Messages[j], page.Messages[i]
	}
	return page, nil
}

func (s jsonlMessageStore) AllMediaPage(kind mediaKind, before *messageCursor, limit int) (messagePage, error) {
	return s.allPage(before, limit, func(message *discordgo.Message) bool {
		if kind == mediaEmbed {
			return len(message.Embeds) > 0
		}
		for _, attachment := range message.Attachments {
			if attachmentMediaKind(attachment) == kind {
				return true
			}
		}
		return false
	})
}

func attachmentMediaKind(attachment *discordgo.MessageAttachment) mediaKind {
	contentType := attachment.ContentType
	parsedOK := false
	if parsed, _, err := mime.ParseMediaType(contentType); err == nil {
		contentType = parsed
		parsedOK = contentType != ""
	}
	if !parsedOK || strings.EqualFold(contentType, "application/octet-stream") {
		contentType = mime.TypeByExtension(filepath.Ext(attachment.Filename))
		if parsed, _, err := mime.ParseMediaType(contentType); err == nil {
			contentType = parsed
		}
	}
	switch {
	case strings.HasPrefix(strings.ToLower(contentType), "image/"):
		return mediaImage
	case strings.HasPrefix(strings.ToLower(contentType), "video/"):
		return mediaVideo
	case strings.HasPrefix(strings.ToLower(contentType), "audio/"):
		return mediaAudio
	default:
		return mediaFile
	}
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
		messages, err := loadArchivedMessages(s.root, date, channelID)
		if err != nil {
			return messagePage{}, err
		}
		for j := len(messages) - 1; j >= 0 && len(collected) <= limit; j-- {
			archived := messages[j]
			message := archived.Message
			if before != nil && date == before.Date && !messageBefore(message, cursorTime, before.ID) {
				continue
			}
			if include != nil && !include(message) {
				continue
			}
			collected = append(collected, archived)
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

func (s jsonlMessageStore) allPage(before *messageCursor, limit int, include func(*discordgo.Message) bool) (messagePage, error) {
	if limit <= 0 {
		return messagePage{}, nil
	}
	dates, err := listAllDates(s.root)
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

	collected := make([]archivedMessage, 0, limit+1)
	for i := len(dates) - 1; i >= 0 && len(collected) <= limit; i-- {
		date := dates[i]
		if before != nil && date > before.Date {
			continue
		}
		messages, err := loadAllMessages(s.root, date)
		if err != nil {
			return messagePage{}, err
		}
		for j := len(messages) - 1; j >= 0 && len(collected) <= limit; j-- {
			archived := messages[j]
			if before != nil && date == before.Date && !messageBefore(archived.Message, cursorTime, before.ID) {
				continue
			}
			if include != nil && !include(archived.Message) {
				continue
			}
			collected = append(collected, archived)
		}
	}

	hasMore := len(collected) > limit
	if hasMore {
		collected = collected[:limit]
	}
	page := messagePage{Messages: collected, HasMore: hasMore}
	if hasMore && len(collected) > 0 {
		oldest := collected[len(collected)-1]
		page.NextCursor = &messageCursor{Date: oldest.Date, Timestamp: oldest.Message.Timestamp.Format(time.RFC3339Nano), ID: oldest.Message.ID}
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

// container holds the channel metadata needed by the web app. It includes
// categories and forum parents as well as message-bearing channels and
// threads so the channel list can reproduce Discord's hierarchy.
type container struct {
	ID                 string
	Name               string
	Type               discordgo.ChannelType
	ParentID           string
	Position           int
	IsThread           bool
	CanContainMessages bool
	LastMessageID      string
	Source             string // thread archive source; empty for regular channels
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
		containers = append(containers, container{
			ID:                 channel.ID,
			Name:               channel.Name,
			Type:               channel.Type,
			ParentID:           channel.ParentID,
			Position:           channel.Position,
			CanContainMessages: archiveformat.CanContainMessages(channel.Type),
			LastMessageID:      channel.LastMessageID,
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
			ID:                 line.Thread.ID,
			Name:               line.Thread.Name,
			Type:               line.Thread.Type,
			ParentID:           line.Thread.ParentID,
			Position:           line.Thread.Position,
			IsThread:           true,
			CanContainMessages: true,
			LastMessageID:      line.Thread.LastMessageID,
			Source:             line.Source,
		})
	}
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
var channelDirPattern = regexp.MustCompile(`^channel_id=(.+)$`)

func listAllDates(root string) ([]string, error) {
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
		if entry.IsDir() {
			if match := dateDirPattern.FindStringSubmatch(entry.Name()); match != nil {
				dates = append(dates, match[1])
			}
		}
	}
	sort.Strings(dates)
	return dates, nil
}

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
	archived, err := loadArchivedMessages(root, date, channelID)
	if err != nil {
		return nil, err
	}
	messages := make([]*discordgo.Message, 0, len(archived))
	for _, item := range archived {
		messages = append(messages, item.Message)
	}
	return messages, nil
}

func loadArchivedMessages(root, date, channelID string) ([]archivedMessage, error) {
	path := filepath.Join(root, "messages", "date="+date, "channel_id="+channelID, "messages.jsonl")

	var messages []archivedMessage
	err := decodeJSONLines(path, func(line messageLine) {
		if line.Message != nil {
			id := line.ChannelID
			if id == "" {
				id = channelID
			}
			messages = append(messages, archivedMessage{GuildID: line.GuildID, Date: date, ChannelID: id, ChannelName: line.ChannelName, Message: line.Message})
		}
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(messages, func(i, j int) bool {
		if messages[i].Message.Timestamp.Equal(messages[j].Message.Timestamp) {
			return messages[i].Message.ID < messages[j].Message.ID
		}
		return messages[i].Message.Timestamp.Before(messages[j].Message.Timestamp)
	})
	return messages, nil
}

func loadAllMessages(root, date string) ([]archivedMessage, error) {
	dateRoot := filepath.Join(root, "messages", "date="+date)
	entries, err := os.ReadDir(dateRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read date directory: %w", err)
	}
	var messages []archivedMessage
	for _, entry := range entries {
		match := channelDirPattern.FindStringSubmatch(entry.Name())
		if !entry.IsDir() || match == nil {
			continue
		}
		channelMessages, err := loadArchivedMessages(root, date, match[1])
		if err != nil {
			return nil, err
		}
		messages = append(messages, channelMessages...)
	}
	sort.Slice(messages, func(i, j int) bool {
		if messages[i].Message.Timestamp.Equal(messages[j].Message.Timestamp) {
			return messages[i].Message.ID < messages[j].Message.ID
		}
		return messages[i].Message.Timestamp.Before(messages[j].Message.Timestamp)
	})
	return messages, nil
}

// attachmentURL builds the path (under the /files/ route) an attachment is
// served at.
func attachmentURL(guildID, date, channelID, messageID string, attachment *discordgo.MessageAttachment) string {
	relPath := archiveformat.AttachmentRelPath(date, channelID, messageID, attachment)
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
	relPath := archiveformat.AttachmentRelPath(date, channelID, messageID, attachment)
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
