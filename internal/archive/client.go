package archive

import (
	"fmt"
	"io"
	"net/http"
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
	// DownloadAttachment fetches attachment content from its CDN URL. Unlike
	// the other methods this is a plain HTTP GET, not a Discord API call.
	DownloadAttachment(url string) (io.ReadCloser, error)
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

// DownloadAttachment reuses the session's HTTP client so it inherits the same
// timeout/proxy configuration as the rest of the archiver's requests, even
// though CDN downloads are unauthenticated plain HTTP GETs.
func (c *sessionClient) DownloadAttachment(url string) (io.ReadCloser, error) {
	resp, err := c.session.Client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("get %s: unexpected status %s", url, resp.Status)
	}
	return resp.Body, nil
}

func (c *sessionClient) Close() error {
	return c.session.Close()
}
