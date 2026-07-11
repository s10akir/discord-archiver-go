package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/s10akir/discord-archiver-go/pkg/archiveformat"
)

func dbGuilds(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM guilds ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

func dbContainers(ctx context.Context, db *sql.DB, guildID string) ([]container, map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id,name,type,parent_id,position,is_thread,source,last_message_id FROM channels WHERE guild_id=$1`, guildID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var result []container
	names := make(map[string]string)
	for rows.Next() {
		var item container
		if err := rows.Scan(&item.ID, &item.Name, &item.Type, &item.ParentID, &item.Position, &item.IsThread, &item.Source, &item.LastMessageID); err != nil {
			return nil, nil, err
		}
		item.CanContainMessages = item.IsThread || archiveformat.CanContainMessages(item.Type)
		names[item.ID] = item.Name
		result = append(result, item)
	}
	return result, names, rows.Err()
}

type postgresMessageStore struct {
	db      *sql.DB
	ctx     context.Context
	guildID string
}

func (s postgresMessageStore) Page(channelID string, before *messageCursor, limit int) (messagePage, error) {
	page, err := s.query(channelID, "", before, limit)
	if err == nil {
		reverseArchivedMessages(page.Messages)
	}
	return page, err
}
func (s postgresMessageStore) AllPage(before *messageCursor, limit int) (messagePage, error) {
	page, err := s.query("", "", before, limit)
	if err == nil {
		reverseArchivedMessages(page.Messages)
	}
	return page, err
}
func (s postgresMessageStore) MediaPage(channelID string, kind mediaKind, before *messageCursor, limit int) (messagePage, error) {
	return s.query(channelID, string(kind), before, limit)
}
func (s postgresMessageStore) AllMediaPage(kind mediaKind, before *messageCursor, limit int) (messagePage, error) {
	return s.query("", string(kind), before, limit)
}

func reverseArchivedMessages(items []archivedMessage) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

func (s postgresMessageStore) query(channelID, kind string, before *messageCursor, limit int) (messagePage, error) {
	args := []any{s.guildID}
	where := []string{"m.guild_id=$1"}
	if channelID != "" {
		args = append(args, channelID)
		where = append(where, fmt.Sprintf("m.channel_id=$%d", len(args)))
	}
	if before != nil {
		cursorTime, err := time.Parse(time.RFC3339Nano, before.Timestamp)
		if err != nil {
			return messagePage{}, err
		}
		args = append(args, cursorTime, before.ID)
		where = append(where, fmt.Sprintf("(m.timestamp,m.id) < ($%d,$%d)", len(args)-1, len(args)))
	}
	switch kind {
	case string(mediaEmbed):
		where = append(where, "m.has_embeds")
	case string(mediaImage), string(mediaVideo), string(mediaAudio):
		prefix := strings.TrimSuffix(kind, "s") + "/"
		args = append(args, prefix+"%")
		where = append(where, fmt.Sprintf("EXISTS (SELECT 1 FROM attachments a WHERE a.guild_id=m.guild_id AND a.message_id=m.id AND lower(a.content_type) LIKE $%d)", len(args)))
	case string(mediaFile):
		where = append(where, "m.has_attachments")
	}
	args = append(args, limit+1)
	query := `SELECT m.archive_date::text,m.channel_id,COALESCE(c.name,''),m.payload FROM messages m LEFT JOIN channels c ON c.guild_id=m.guild_id AND c.id=m.channel_id WHERE ` + strings.Join(where, " AND ") + fmt.Sprintf(` ORDER BY m.timestamp DESC,m.id DESC LIMIT $%d`, len(args))
	rows, err := s.db.QueryContext(s.ctx, query, args...)
	if err != nil {
		return messagePage{}, err
	}
	defer rows.Close()
	var items []archivedMessage
	for rows.Next() {
		var item archivedMessage
		var payload []byte
		if err := rows.Scan(&item.Date, &item.ChannelID, &item.ChannelName, &payload); err != nil {
			return messagePage{}, err
		}
		var message discordgo.Message
		if err := json.Unmarshal(payload, &message); err != nil {
			return messagePage{}, err
		}
		item.Message = &message
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return messagePage{}, err
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	page := messagePage{Messages: items, HasMore: hasMore}
	if hasMore && len(items) > 0 {
		oldest := items[len(items)-1]
		page.NextCursor = &messageCursor{Date: oldest.Date, Timestamp: oldest.Message.Timestamp.Format(time.RFC3339Nano), ID: oldest.Message.ID}
	}
	return page, nil
}
