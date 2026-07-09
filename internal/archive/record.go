package archive

import "github.com/bwmarrin/discordgo"

type archiveRecord struct {
	GuildID     string                `json:"guild_id"`
	ChannelID   string                `json:"channel_id"`
	ChannelName string                `json:"channel_name,omitempty"`
	ChannelType discordgo.ChannelType `json:"channel_type"`
	ParentID    string                `json:"parent_id,omitempty"`
	Message     *discordgo.Message    `json:"message"`
}

type channelRecord struct {
	GuildID string             `json:"guild_id"`
	Channel *discordgo.Channel `json:"channel"`
}

type threadRecord struct {
	GuildID string             `json:"guild_id"`
	Source  string             `json:"source"`
	Thread  *discordgo.Channel `json:"thread"`
}
