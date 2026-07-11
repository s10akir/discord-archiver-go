package database

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/s10akir/discord-archiver-go/pkg/archiveformat"
)

type MetadataRecord struct {
	Kind    string                       `json:"kind"`
	Channel *archiveformat.ChannelRecord `json:"channel,omitempty"`
	Thread  *archiveformat.ThreadRecord  `json:"thread,omitempty"`
}

func ReplaceMetadata(ctx context.Context, db *sql.DB, guildID string, reader io.Reader) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO guilds(id) VALUES($1) ON CONFLICT(id) DO UPDATE SET updated_at=now()`, guildID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM channels WHERE guild_id=$1`, guildID); err != nil {
		return err
	}
	return scanNDJSON(reader, func(line []byte) error {
		var record MetadataRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return err
		}
		var channel *discordgo.Channel
		var source string
		var isThread bool
		switch record.Kind {
		case "channel":
			if record.Channel == nil || record.Channel.GuildID != guildID {
				return fmt.Errorf("channel guild does not match path")
			}
			channel = record.Channel.Channel
		case "thread":
			if record.Thread == nil || record.Thread.GuildID != guildID {
				return fmt.Errorf("thread guild does not match path")
			}
			channel, source, isThread = record.Thread.Thread, record.Thread.Source, true
		default:
			return fmt.Errorf("unknown metadata kind %q", record.Kind)
		}
		if channel == nil || channel.ID == "" {
			return fmt.Errorf("metadata channel id is required")
		}
		payload, err := json.Marshal(channel)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO channels(guild_id,id,name,type,parent_id,position,is_thread,source,last_message_id,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, guildID, channel.ID, channel.Name, channel.Type, channel.ParentID, channel.Position, isThread, source, channel.LastMessageID, payload)
		return err
	}, func() error { return tx.Commit() })
}

func ReplaceDate(ctx context.Context, db *sql.DB, guildID string, date time.Time, reader io.Reader) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE affected_authors(author_id text PRIMARY KEY) ON COMMIT DROP`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO guilds(id) VALUES($1) ON CONFLICT(id) DO UPDATE SET updated_at=now()`, guildID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO affected_authors(author_id) SELECT DISTINCT author_id FROM messages WHERE guild_id=$1 AND archive_date=$2 AND author_id<>'' ON CONFLICT DO NOTHING`, guildID, date); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE guild_id=$1 AND archive_date=$2`, guildID, date); err != nil {
		return err
	}
	return scanNDJSON(reader, func(line []byte) error {
		var record archiveformat.MessageRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return err
		}
		if record.GuildID != guildID || record.Message == nil || record.Message.ID == "" || record.ChannelID == "" {
			return fmt.Errorf("message identity does not match path")
		}
		if record.Message.Timestamp.IsZero() {
			return fmt.Errorf("message timestamp is required")
		}
		payload, err := json.Marshal(record.Message)
		if err != nil {
			return err
		}
		authorID, authorName := "", ""
		if record.Message.Author != nil {
			authorID, authorName = record.Message.Author.ID, record.Message.Author.DisplayName()
		}
		var embedParts []string
		for _, embed := range record.Message.Embeds {
			if embed != nil {
				embedParts = append(embedParts, embed.Title, embed.Description)
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO messages(guild_id,id,channel_id,archive_date,timestamp,author_id,author_name,content,has_attachments,has_embeds,embed_text,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(guild_id,id) DO UPDATE SET channel_id=excluded.channel_id,archive_date=excluded.archive_date,timestamp=excluded.timestamp,author_id=excluded.author_id,author_name=excluded.author_name,content=excluded.content,has_attachments=excluded.has_attachments,has_embeds=excluded.has_embeds,embed_text=excluded.embed_text,payload=excluded.payload`, guildID, record.Message.ID, record.ChannelID, date, record.Message.Timestamp, authorID, authorName, record.Message.Content, len(record.Message.Attachments) > 0, len(record.Message.Embeds) > 0, strings.Join(embedParts, "\n"), payload)
		if err != nil {
			return err
		}
		if authorID != "" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO affected_authors(author_id) VALUES($1) ON CONFLICT DO NOTHING`, authorID); err != nil {
				return err
			}
		}
		for _, attachment := range record.Message.Attachments {
			if attachment == nil || attachment.ID == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO attachments(guild_id,message_id,id,filename,content_type,size) VALUES($1,$2,$3,$4,$5,$6)`, guildID, record.Message.ID, attachment.ID, attachment.Filename, attachment.ContentType, attachment.Size); err != nil {
				return err
			}
		}
		return nil
	}, func() error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM authors a USING affected_authors affected WHERE a.guild_id=$1 AND a.author_id=affected.author_id`, guildID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO authors(guild_id,author_id,display_name,observed_at) SELECT DISTINCT ON (m.author_id) m.guild_id,m.author_id,m.author_name,m.timestamp FROM messages m JOIN affected_authors affected ON affected.author_id=m.author_id WHERE m.guild_id=$1 ORDER BY m.author_id,m.timestamp DESC,m.id DESC`, guildID); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func scanNDJSON(reader io.Reader, each func([]byte) error, done func() error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		if len(strings.TrimSpace(scanner.Text())) == 0 {
			continue
		}
		if err := each(scanner.Bytes()); err != nil {
			return fmt.Errorf("line %d: %w", line, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return done()
}
