// Package archiveformat defines the on-disk contract shared by the archiver
// and read-only consumers of its archives.
package archiveformat

import (
	"path/filepath"
	"regexp"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
)

type MessageRecord struct {
	GuildID     string                `json:"guild_id"`
	ChannelID   string                `json:"channel_id"`
	ChannelName string                `json:"channel_name,omitempty"`
	ChannelType discordgo.ChannelType `json:"channel_type"`
	ParentID    string                `json:"parent_id,omitempty"`
	Message     *discordgo.Message    `json:"message"`
}

type ChannelRecord struct {
	GuildID string             `json:"guild_id"`
	Channel *discordgo.Channel `json:"channel"`
}

type ThreadRecord struct {
	GuildID string             `json:"guild_id"`
	Source  string             `json:"source"`
	Thread  *discordgo.Channel `json:"thread"`
}

func CanContainMessages(channelType discordgo.ChannelType) bool {
	switch channelType {
	case discordgo.ChannelTypeGuildText,
		discordgo.ChannelTypeGuildNews,
		discordgo.ChannelTypeGuildVoice,
		discordgo.ChannelTypeGuildNewsThread,
		discordgo.ChannelTypeGuildPublicThread,
		discordgo.ChannelTypeGuildPrivateThread:
		return true
	default:
		return false
	}
}

var unsafeFilenamePattern = regexp.MustCompile(`[/\\\x00]`)

const maxAttachmentNameBytes = 255

// AttachmentRelPath returns an attachment's path relative to a guild root.
func AttachmentRelPath(date, channelID, messageID string, attachment *discordgo.MessageAttachment) string {
	name := attachmentFileName(attachment.ID, attachment.Filename)
	return filepath.Join("messages", "date="+date, "channel_id="+channelID, "attachments", messageID, name)
}

func attachmentFileName(id, filename string) string {
	prefix := id + "-"
	name := filepath.Base(filename)
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "file"
	}
	name = unsafeFilenamePattern.ReplaceAllString(name, "_")
	budget := maxAttachmentNameBytes - len(prefix)
	if budget <= 0 {
		return truncateBytes(prefix, maxAttachmentNameBytes)
	}
	return prefix + truncateWithExt(name, budget)
}

func truncateWithExt(name string, maxBytes int) string {
	if len(name) <= maxBytes {
		return name
	}
	ext := filepath.Ext(name)
	if len(ext) >= maxBytes {
		return truncateBytes(name, maxBytes)
	}
	return truncateBytes(name[:len(name)-len(ext)], maxBytes-len(ext)) + ext
}

func truncateBytes(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	for maxBytes > 0 {
		r, size := utf8.DecodeLastRuneInString(value[:maxBytes])
		if r != utf8.RuneError || size != 1 {
			break
		}
		maxBytes--
	}
	return value[:maxBytes]
}
