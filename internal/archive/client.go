package archive

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
)

// discordClient is the fetch-side boundary: the subset of Discord operations
// the archiver needs. Keeping the archiver on this interface (instead of
// *discordgo.Session) lets tests fake the source and keeps fetch concerns
// separate from output concerns.
type discordClient interface {
	GuildChannels(guildID string) ([]*discordgo.Channel, error)
	GuildThreadsActive(guildID string) (*discordgo.ThreadsList, error)
	ThreadsArchived(channelID string, before *time.Time, limit int) (*discordgo.ThreadsList, error)
	ThreadsPrivateArchived(channelID string, before *time.Time, limit int) (*discordgo.ThreadsList, error)
	ChannelMessages(channelID string, limit int, beforeID string) ([]*discordgo.Message, error)
	Close() error
}

type sessionClient struct {
	session *discordgo.Session
}

func newSessionClient(token string) (*sessionClient, error) {
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("create discord session: %w", err)
	}
	return &sessionClient{session: session}, nil
}

func (c *sessionClient) GuildChannels(guildID string) ([]*discordgo.Channel, error) {
	return c.session.GuildChannels(guildID)
}

func (c *sessionClient) GuildThreadsActive(guildID string) (*discordgo.ThreadsList, error) {
	return c.session.GuildThreadsActive(guildID)
}

func (c *sessionClient) ThreadsArchived(channelID string, before *time.Time, limit int) (*discordgo.ThreadsList, error) {
	return c.session.ThreadsArchived(channelID, before, limit)
}

func (c *sessionClient) ThreadsPrivateArchived(channelID string, before *time.Time, limit int) (*discordgo.ThreadsList, error) {
	return c.session.ThreadsPrivateArchived(channelID, before, limit)
}

func (c *sessionClient) ChannelMessages(channelID string, limit int, beforeID string) ([]*discordgo.Message, error) {
	return channelMessagesCompat(c.session, channelID, limit, beforeID, "", "")
}

func (c *sessionClient) Close() error {
	return c.session.Close()
}
