package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	appdb "github.com/s10akir/discord-archiver-go/apps/web/internal/database"
)

type server struct {
	archiveDir      string
	db              *sql.DB
	newMessageStore func(root string) messageStore
}

func (s *server) guilds(ctx context.Context) ([]string, error) {
	if s.db != nil {
		return dbGuilds(ctx, s.db)
	}
	return listGuilds(s.archiveDir)
}

func (s *server) containers(ctx context.Context, guildID string) ([]container, map[string]string, error) {
	if s.db != nil {
		return dbContainers(ctx, s.db, guildID)
	}
	return loadContainers(guildRoot(s.archiveDir, guildID))
}

const messagePageSize = 100

// NewHandler builds the web application's HTTP handler rooted at archiveDir, the same
// -out-dir passed to `dump`/the daemon.
func NewHandler(archiveDir string) (http.Handler, error) {
	return newHandler(archiveDir, nil)
}

func newHandler(archiveDir string, db *sql.DB) (http.Handler, error) {
	info, err := os.Stat(archiveDir)
	if err != nil {
		return nil, fmt.Errorf("open archive directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", archiveDir)
	}

	s := &server{
		archiveDir: archiveDir,
		db:         db,
		newMessageStore: func(guildID string) messageStore {
			return jsonlMessageStore{root: guildRoot(archiveDir, guildID)}
		},
	}
	if db != nil {
		s.newMessageStore = func(guildID string) messageStore {
			return postgresMessageStore{db: db, ctx: context.Background(), guildID: guildID}
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleGuilds)
	mux.HandleFunc("GET /g/{guild}", s.handleChannels)
	mux.HandleFunc("GET /g/{guild}/all", s.handleAllMessages)
	mux.HandleFunc("GET /g/{guild}/all/messages", s.handleAllMessagePage)
	mux.HandleFunc("GET /g/{guild}/c/{channel}", s.handleMessages)
	mux.HandleFunc("GET /g/{guild}/c/{channel}/messages", s.handleMessagePage)
	for _, kind := range mediaKinds {
		mux.HandleFunc("GET /g/{guild}/all/"+string(kind.Kind), s.handleAllMedia(kind))
		mux.HandleFunc("GET /g/{guild}/all/"+string(kind.Kind)+"/items", s.handleAllMediaPage(kind))
		mux.HandleFunc("GET /g/{guild}/c/{channel}/"+string(kind.Kind), s.handleMedia(kind))
		mux.HandleFunc("GET /g/{guild}/c/{channel}/"+string(kind.Kind)+"/items", s.handleMediaPage(kind))
	}
	mux.HandleFunc("GET /files/{guild}/{rest...}", s.handleFile)
	if db != nil {
		mux.HandleFunc("GET /search", s.handleSearch)
	}
	if db != nil {
		mux.HandleFunc("PUT /api/v1/import/guilds/{guild}/metadata", s.handleImportMetadata)
		mux.HandleFunc("PUT /api/v1/import/guilds/{guild}/dates/{date}", s.handleImportDate)
	}
	return mux, nil
}

var searchTemplate = template.Must(template.New("search").Parse(`<!doctype html><html lang="ja"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>アーカイブ検索</title><style>` + baseCSS + `
.search-form{display:grid;grid-template-columns:repeat(auto-fit,minmax(160px,1fr));gap:10px;background:#2b2d31;padding:14px;border-radius:8px;margin-bottom:18px}.search-form label{font-size:12px;color:#b5bac1}.search-form input,.search-form select{display:block;width:100%;margin-top:4px;padding:8px;background:#1e1f22;color:#fff;border:1px solid #4a4d53;border-radius:4px}.search-form button{align-self:end;padding:9px;background:#5865f2;color:white;border:0;border-radius:4px}</style></head><body><header><h1>アーカイブ検索</h1><div class="crumbs"><a href="/">アーカイブ</a></div></header><main><form class="search-form" method="get"><label>キーワード<input name="q" value="{{.Query}}"></label><label>Guild ID<input name="guild" value="{{.Guild}}"></label><label>Channel / Thread ID<input name="channel" value="{{.Channel}}"></label><label>投稿者<input name="author" value="{{.Author}}"></label><label>開始日時<input type="datetime-local" name="from" value="{{.From}}"></label><label>終了日時<input type="datetime-local" name="to" value="{{.To}}"></label><label>添付<select name="attachment"><option value="">指定なし</option><option value="yes">あり</option><option value="no">なし</option></select></label><label>メディア<select name="media"><option value="">指定なし</option><option value="image">画像</option><option value="video">動画</option><option value="audio">音声</option><option value="embed">埋め込み</option></select></label><label>埋め込み<select name="embed"><option value="">指定なし</option><option value="yes">あり</option><option value="no">なし</option></select></label><button>検索</button></form>{{.Results}}</main></body></html>`))

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	parseTime := func(value string) time.Time { parsed, _ := time.Parse("2006-01-02T15:04", value); return parsed }
	filter := searchFilter{GuildID: q.Get("guild"), ChannelID: q.Get("channel"), Author: q.Get("author"), Query: q.Get("q"), Media: q.Get("media"), Attachment: q.Get("attachment"), Embed: q.Get("embed"), From: parseTime(q.Get("from")), To: parseTime(q.Get("to"))}
	messages, err := searchMessages(r.Context(), s.db, filter, messagePageSize)
	if err != nil {
		httpError(w, err)
		return
	}
	names := map[string]string{}
	if filter.GuildID != "" {
		_, names, _ = s.containers(r.Context(), filter.GuildID)
	}
	var results bytes.Buffer
	if len(messages) == 0 {
		results.WriteString(`<p class="empty">条件に一致するメッセージはありません。</p>`)
	} else {
		sections := buildMessageSections(guildRoot(s.archiveDir, filter.GuildID), filter.GuildID, messages, names, true)
		if err := messagesTemplate.ExecuteTemplate(&results, "sections", sections); err != nil {
			httpError(w, err)
			return
		}
	}
	data := struct {
		Query, Guild, Channel, Author, From, To string
		Results                                 template.HTML
	}{q.Get("q"), q.Get("guild"), q.Get("channel"), q.Get("author"), q.Get("from"), q.Get("to"), template.HTML(results.String())}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := searchTemplate.Execute(w, data); err != nil {
		log.Printf("render search: %v", err)
	}
}

// Run serves the web application on addr until ctx is cancelled.
func Run(ctx context.Context, archiveDir, databaseURL, addr string) error {
	db, err := appdb.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	handler, err := newHandler(archiveDir, db)
	if err != nil {
		return err
	}

	server := &http.Server{Addr: addr, Handler: handler}
	errCh := make(chan error, 1)
	go func() {
		log.Printf("web listening on %s (archive: %s)", addr, archiveDir)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-errCh
	case err := <-errCh:
		return err
	}
}

func (s *server) handleImportMetadata(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 512<<20)
	if err := appdb.ReplaceMetadata(r.Context(), s.db, r.PathValue("guild"), r.Body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleImportDate(w http.ResponseWriter, r *http.Request) {
	date, err := time.Parse(time.DateOnly, r.PathValue("date"))
	if err != nil {
		http.Error(w, "invalid date", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<30)
	if err := appdb.ReplaceDate(r.Context(), s.db, r.PathValue("guild"), date, r.Body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleGuilds(w http.ResponseWriter, r *http.Request) {
	guilds, err := s.guilds(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	if len(guilds) == 1 {
		http.Redirect(w, r, "/g/"+guilds[0], http.StatusFound)
		return
	}
	render(w, guildsTemplate, struct{ Guilds []string }{guilds})
}

type channelGroup struct {
	ID            string
	Name          string
	Items         []channelItem
	Position      int
	Uncategorized bool
}

type channelItem struct {
	Channel container
	Threads []container
	HasLink bool
}

func (s *server) handleChannels(w http.ResponseWriter, r *http.Request) {
	guildID := r.PathValue("guild")
	containers, names, err := s.containers(r.Context(), guildID)
	if err != nil {
		httpError(w, err)
		return
	}

	channels := make(map[string]container)
	threadsByParent := make(map[string][]container)
	for _, c := range containers {
		if c.IsThread {
			threadsByParent[c.ParentID] = append(threadsByParent[c.ParentID], c)
		} else {
			channels[c.ID] = c
		}
	}
	for parentID := range threadsByParent {
		sortThreadsByActivity(threadsByParent[parentID])
	}

	groupsByCategory := make(map[string]*channelGroup)
	uncategorized := &channelGroup{Name: "その他", Uncategorized: true}
	for _, channel := range channels {
		if channel.Type == discordgo.ChannelTypeGuildCategory {
			groupsByCategory[channel.ID] = &channelGroup{ID: channel.ID, Name: channel.Name, Position: channel.Position}
		}
	}
	for _, channel := range channels {
		if channel.Type == discordgo.ChannelTypeGuildCategory {
			continue
		}
		threads := threadsByParent[channel.ID]
		if !channel.CanContainMessages && len(threads) == 0 {
			continue
		}
		item := channelItem{Channel: channel, Threads: threads, HasLink: channel.CanContainMessages}
		if group := groupsByCategory[channel.ParentID]; group != nil {
			group.Items = append(group.Items, item)
		} else {
			uncategorized.Items = append(uncategorized.Items, item)
		}
		delete(threadsByParent, channel.ID)
	}
	// Preserve threads whose parent metadata is absent by nesting them under a
	// non-linkable placeholder instead of promoting them to regular channels.
	for parentID, threads := range threadsByParent {
		name := names[parentID]
		if name == "" {
			name = "不明なチャンネル"
		}
		uncategorized.Items = append(uncategorized.Items, channelItem{
			Channel: container{ID: parentID, Name: name}, Threads: threads,
		})
	}

	var groups []channelGroup
	if len(uncategorized.Items) > 0 {
		sortChannelItems(uncategorized.Items)
		groups = append(groups, *uncategorized)
	}
	for _, group := range groupsByCategory {
		if len(group.Items) == 0 {
			continue
		}
		sortChannelItems(group.Items)
		groups = append(groups, *group)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Uncategorized != groups[j].Uncategorized {
			return groups[i].Uncategorized
		}
		if groups[i].Position != groups[j].Position {
			return groups[i].Position < groups[j].Position
		}
		return groups[i].ID < groups[j].ID
	})

	render(w, channelsTemplate, struct {
		GuildID string
		Groups  []channelGroup
	}{guildID, groups})
}

type messagePageView struct {
	GuildID  string
	Channel  container
	Title    string
	BasePath string
	All      bool
	Sections []messageSection
	Cursor   string
	HasMore  bool
}

func sortChannelItems(items []channelItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Channel.Position != items[j].Channel.Position {
			return items[i].Channel.Position < items[j].Channel.Position
		}
		return items[i].Channel.ID < items[j].Channel.ID
	})
}

func sortThreadsByActivity(threads []container) {
	sort.Slice(threads, func(i, j int) bool {
		left, right := threads[i].LastMessageID, threads[j].LastMessageID
		if left == "" {
			left = threads[i].ID
		}
		if right == "" {
			right = threads[j].ID
		}
		if len(left) != len(right) {
			return len(left) > len(right)
		}
		if left != right {
			return left > right
		}
		return threads[i].ID > threads[j].ID
	})
}

type messageSection struct {
	Date     string
	Messages []messageView
}

func (s *server) handleMessages(w http.ResponseWriter, r *http.Request) {
	guildID := r.PathValue("guild")
	channelID := r.PathValue("channel")
	root := guildRoot(s.archiveDir, guildID)

	containers, names, err := s.containers(r.Context(), guildID)
	if err != nil {
		httpError(w, err)
		return
	}
	channel, ok := findContainer(containers, channelID)
	if !ok {
		channel = container{ID: channelID, Name: channelID}
	}

	page, err := s.newMessageStore(guildID).Page(channelID, nil, messagePageSize)
	if err != nil {
		httpError(w, err)
		return
	}
	sections := buildMessageSections(root, guildID, page.Messages, names, false)

	render(w, messagesTemplate, messagePageView{
		GuildID: guildID, Channel: channel, Title: channel.Name,
		BasePath: "/g/" + guildID + "/c/" + channelID,
		Sections: sections, Cursor: encodeCursor(page.NextCursor), HasMore: page.HasMore,
	})
}

func (s *server) handleAllMessages(w http.ResponseWriter, r *http.Request) {
	guildID := r.PathValue("guild")
	root := guildRoot(s.archiveDir, guildID)
	_, names, err := s.containers(r.Context(), guildID)
	if err != nil {
		httpError(w, err)
		return
	}
	page, err := s.newMessageStore(guildID).AllPage(nil, messagePageSize)
	if err != nil {
		httpError(w, err)
		return
	}
	render(w, messagesTemplate, messagePageView{
		GuildID: guildID, Title: "全チャンネル", BasePath: "/g/" + guildID + "/all", All: true,
		Sections: buildMessageSections(root, guildID, page.Messages, names, true),
		Cursor:   encodeCursor(page.NextCursor), HasMore: page.HasMore,
	})
}

func (s *server) handleMessagePage(w http.ResponseWriter, r *http.Request) {
	guildID := r.PathValue("guild")
	channelID := r.PathValue("channel")
	root := guildRoot(s.archiveDir, guildID)

	cursor, err := decodeCursor(r.URL.Query().Get("before"))
	if err != nil {
		writeJSONError(w, "invalid cursor", http.StatusBadRequest)
		return
	}
	_, names, err := s.containers(r.Context(), guildID)
	if err != nil {
		log.Printf("viewer error: %v", err)
		writeJSONError(w, "internal error", http.StatusInternalServerError)
		return
	}
	page, err := s.newMessageStore(guildID).Page(channelID, cursor, messagePageSize)
	if err != nil {
		log.Printf("viewer error: %v", err)
		writeJSONError(w, "internal error", http.StatusInternalServerError)
		return
	}
	sections := buildMessageSections(root, guildID, page.Messages, names, false)
	var fragment bytes.Buffer
	if err := messagesTemplate.ExecuteTemplate(&fragment, "sections", sections); err != nil {
		log.Printf("viewer error: %v", err)
		writeJSONError(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(struct {
		HTML       string `json:"html"`
		NextCursor string `json:"next_cursor"`
		HasMore    bool   `json:"has_more"`
	}{fragment.String(), encodeCursor(page.NextCursor), page.HasMore}); err != nil {
		log.Printf("encode message page: %v", err)
	}
}

func (s *server) handleAllMessagePage(w http.ResponseWriter, r *http.Request) {
	guildID := r.PathValue("guild")
	root := guildRoot(s.archiveDir, guildID)
	cursor, err := decodeCursor(r.URL.Query().Get("before"))
	if err != nil {
		writeJSONError(w, "invalid cursor", http.StatusBadRequest)
		return
	}
	_, names, err := s.containers(r.Context(), guildID)
	if err != nil {
		log.Printf("viewer error: %v", err)
		writeJSONError(w, "internal error", http.StatusInternalServerError)
		return
	}
	page, err := s.newMessageStore(guildID).AllPage(cursor, messagePageSize)
	if err != nil {
		log.Printf("viewer error: %v", err)
		writeJSONError(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeMessagePageJSON(w, buildMessageSections(root, guildID, page.Messages, names, true), page)
}

func writeMessagePageJSON(w http.ResponseWriter, sections []messageSection, page messagePage) {
	var fragment bytes.Buffer
	if err := messagesTemplate.ExecuteTemplate(&fragment, "sections", sections); err != nil {
		log.Printf("viewer error: %v", err)
		writeJSONError(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(struct {
		HTML       string `json:"html"`
		NextCursor string `json:"next_cursor"`
		HasMore    bool   `json:"has_more"`
	}{fragment.String(), encodeCursor(page.NextCursor), page.HasMore}); err != nil {
		log.Printf("encode message page: %v", err)
	}
}

type mediaItem struct {
	AuthorName  string
	Timestamp   string
	ChannelID   string
	ChannelName string
	ChannelURL  string
	Attachment  *attachmentView
	Embed       *embedView
}

type mediaKindView struct {
	Kind  mediaKind
	Label string
}

var mediaKinds = []mediaKindView{
	{mediaImage, "画像"},
	{mediaVideo, "動画"},
	{mediaAudio, "音声"},
	{mediaFile, "ファイル"},
	{mediaEmbed, "埋め込み"},
}

type mediaPageView struct {
	GuildID  string
	Channel  container
	Title    string
	BasePath string
	All      bool
	Kind     mediaKindView
	Kinds    []mediaKindView
	Items    []mediaItem
	Cursor   string
	HasMore  bool
}

func (s *server) handleMedia(kind mediaKindView) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		guildID := r.PathValue("guild")
		channelID := r.PathValue("channel")
		root := guildRoot(s.archiveDir, guildID)

		containers, names, err := s.containers(r.Context(), guildID)
		if err != nil {
			httpError(w, err)
			return
		}
		channel, ok := findContainer(containers, channelID)
		if !ok {
			channel = container{ID: channelID, Name: channelID}
		}
		page, err := s.newMessageStore(guildID).MediaPage(channelID, kind.Kind, nil, messagePageSize)
		if err != nil {
			httpError(w, err)
			return
		}
		items := buildMediaItems(root, guildID, kind.Kind, page.Messages, names, false)
		render(w, mediaTemplate, mediaPageView{
			GuildID: guildID, Channel: channel, Title: channel.Name,
			BasePath: "/g/" + guildID + "/c/" + channelID,
			Kind:     kind, Kinds: mediaKinds, Items: items,
			Cursor: encodeCursor(page.NextCursor), HasMore: page.HasMore,
		})
	}
}

func (s *server) handleAllMedia(kind mediaKindView) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		guildID := r.PathValue("guild")
		root := guildRoot(s.archiveDir, guildID)
		_, names, err := s.containers(r.Context(), guildID)
		if err != nil {
			httpError(w, err)
			return
		}
		page, err := s.newMessageStore(guildID).AllMediaPage(kind.Kind, nil, messagePageSize)
		if err != nil {
			httpError(w, err)
			return
		}
		render(w, mediaTemplate, mediaPageView{
			GuildID: guildID, Title: "全チャンネル", BasePath: "/g/" + guildID + "/all", All: true,
			Kind: kind, Kinds: mediaKinds,
			Items:  buildMediaItems(root, guildID, kind.Kind, page.Messages, names, true),
			Cursor: encodeCursor(page.NextCursor), HasMore: page.HasMore,
		})
	}
}

func (s *server) handleMediaPage(kind mediaKindView) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		guildID := r.PathValue("guild")
		channelID := r.PathValue("channel")
		root := guildRoot(s.archiveDir, guildID)
		cursor, err := decodeCursor(r.URL.Query().Get("before"))
		if err != nil {
			writeJSONError(w, "invalid cursor", http.StatusBadRequest)
			return
		}
		_, names, err := s.containers(r.Context(), guildID)
		if err != nil {
			log.Printf("viewer error: %v", err)
			writeJSONError(w, "internal error", http.StatusInternalServerError)
			return
		}
		page, err := s.newMessageStore(guildID).MediaPage(channelID, kind.Kind, cursor, messagePageSize)
		if err != nil {
			log.Printf("viewer error: %v", err)
			writeJSONError(w, "internal error", http.StatusInternalServerError)
			return
		}
		items := buildMediaItems(root, guildID, kind.Kind, page.Messages, names, false)
		var fragment bytes.Buffer
		if err := mediaTemplate.ExecuteTemplate(&fragment, "media-items", items); err != nil {
			log.Printf("viewer error: %v", err)
			writeJSONError(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := json.NewEncoder(w).Encode(struct {
			HTML       string `json:"html"`
			NextCursor string `json:"next_cursor"`
			HasMore    bool   `json:"has_more"`
		}{fragment.String(), encodeCursor(page.NextCursor), page.HasMore}); err != nil {
			log.Printf("encode media page: %v", err)
		}
	}
}

func (s *server) handleAllMediaPage(kind mediaKindView) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		guildID := r.PathValue("guild")
		root := guildRoot(s.archiveDir, guildID)
		cursor, err := decodeCursor(r.URL.Query().Get("before"))
		if err != nil {
			writeJSONError(w, "invalid cursor", http.StatusBadRequest)
			return
		}
		_, names, err := s.containers(r.Context(), guildID)
		if err != nil {
			log.Printf("viewer error: %v", err)
			writeJSONError(w, "internal error", http.StatusInternalServerError)
			return
		}
		page, err := s.newMessageStore(guildID).AllMediaPage(kind.Kind, cursor, messagePageSize)
		if err != nil {
			log.Printf("viewer error: %v", err)
			writeJSONError(w, "internal error", http.StatusInternalServerError)
			return
		}
		items := buildMediaItems(root, guildID, kind.Kind, page.Messages, names, true)
		var fragment bytes.Buffer
		if err := mediaTemplate.ExecuteTemplate(&fragment, "media-items", items); err != nil {
			log.Printf("viewer error: %v", err)
			writeJSONError(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(struct {
			HTML       string `json:"html"`
			NextCursor string `json:"next_cursor"`
			HasMore    bool   `json:"has_more"`
		}{fragment.String(), encodeCursor(page.NextCursor), page.HasMore})
	}
}

func buildMediaItems(root, guildID string, kind mediaKind, messages []archivedMessage, names map[string]string, showChannel bool) []mediaItem {
	var items []mediaItem
	for _, archived := range messages {
		view := buildMessageView(root, guildID, archived, names)
		base := mediaItem{AuthorName: view.AuthorName, Timestamp: view.Timestamp}
		if showChannel {
			base.ChannelID, base.ChannelName = view.ChannelID, view.ChannelName
			base.ChannelURL = "/g/" + guildID + "/c/" + view.ChannelID + "/" + string(kind)
		}
		for i := range view.Attachments {
			if attachmentMediaKind(archived.Message.Attachments[i]) == kind {
				item := base
				item.Attachment = &view.Attachments[i]
				items = append(items, item)
			}
		}
		if kind == mediaEmbed {
			for i := range view.Embeds {
				item := base
				item.Embed = &view.Embeds[i]
				items = append(items, item)
			}
		}
	}
	return items
}

func writeJSONError(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{message})
}

func buildMessageSections(root, guildID string, messages []archivedMessage, names map[string]string, showChannel bool) []messageSection {
	var sections []messageSection
	for _, archived := range messages {
		if len(sections) == 0 || sections[len(sections)-1].Date != archived.Date {
			sections = append(sections, messageSection{Date: archived.Date})
		}
		section := &sections[len(sections)-1]
		view := buildMessageView(root, guildID, archived, names)
		view.ShowChannel = showChannel
		section.Messages = append(section.Messages, view)
	}
	return sections
}

func (s *server) handleFile(w http.ResponseWriter, r *http.Request) {
	guildID := r.PathValue("guild")
	rest := r.PathValue("rest")
	if guildID == "" || rest == "" {
		http.NotFound(w, r)
		return
	}

	root := guildRoot(s.archiveDir, guildID)
	full := filepath.Join(root, filepath.FromSlash(rest))

	relToRoot, err := filepath.Rel(root, full)
	if err != nil || relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
		http.NotFound(w, r)
		return
	}

	http.ServeFile(w, r, full)
}

type messageView struct {
	AuthorID     string
	AuthorName   string
	AvatarURL    string
	Timestamp    string
	Edited       bool
	Content      template.HTML
	ReplySnippet string
	Attachments  []attachmentView
	Embeds       []embedView
	Reactions    []reactionView
	ChannelID    string
	ChannelName  string
	ChannelURL   string
	ShowChannel  bool
}

type attachmentView struct {
	URL       string
	Filename  string
	Size      int
	Width     int
	Height    int
	IsImage   bool
	IsVideo   bool
	IsAudio   bool
	Available bool
}

type embedView struct {
	Title       string
	URL         string
	Description string
	ImageURL    string
	ImageWidth  int
	ImageHeight int
	Color       string
}

type reactionView struct {
	Emoji string
	Count int
}

func buildMessageView(root, guildID string, archived archivedMessage, names map[string]string) messageView {
	m := archived.Message
	channelID := archived.ChannelID
	channelName := archived.ChannelName
	if channelName == "" {
		channelName = names[channelID]
	}
	if channelName == "" {
		channelName = channelID
	}
	v := messageView{
		Timestamp:   m.Timestamp.Local().Format("2006-01-02 15:04:05"),
		Edited:      m.EditedTimestamp != nil,
		ChannelID:   channelID,
		ChannelName: channelName,
		ChannelURL:  "/g/" + guildID + "/c/" + channelID,
	}

	if m.Author != nil {
		v.AuthorID = m.Author.ID
		v.AuthorName = m.Author.DisplayName()
		v.AvatarURL = m.Author.AvatarURL("64")
	} else {
		v.AuthorName = "unknown"
	}

	v.Content = formatContent(m.Content, m.Mentions, names)

	if m.ReferencedMessage != nil {
		snippet := m.ReferencedMessage.Content
		if snippet == "" {
			snippet = "(添付/埋め込み)"
		}
		author := "unknown"
		if m.ReferencedMessage.Author != nil {
			author = m.ReferencedMessage.Author.DisplayName()
		}
		v.ReplySnippet = author + ": " + truncate(snippet, 80)
	}

	for _, a := range m.Attachments {
		av := attachmentView{Filename: a.Filename, Size: a.Size, Width: a.Width, Height: a.Height}
		kind := attachmentMediaKind(a)
		av.IsImage = kind == mediaImage
		av.IsVideo = kind == mediaVideo
		av.IsAudio = kind == mediaAudio
		if hasLocalAttachment(root, archived.Date, channelID, m.ID, a) {
			av.Available = true
			av.URL = attachmentURL(guildID, archived.Date, channelID, m.ID, a)
		}
		v.Attachments = append(v.Attachments, av)
	}

	for _, e := range m.Embeds {
		ev := embedView{
			Title:       e.Title,
			URL:         e.URL,
			Description: truncate(e.Description, 500),
			Color:       embedColor(e.Color),
		}
		if e.Image != nil {
			ev.ImageURL = e.Image.URL
			ev.ImageWidth = e.Image.Width
			ev.ImageHeight = e.Image.Height
		} else if e.Thumbnail != nil {
			ev.ImageURL = e.Thumbnail.URL
			ev.ImageWidth = e.Thumbnail.Width
			ev.ImageHeight = e.Thumbnail.Height
		}
		v.Embeds = append(v.Embeds, ev)
	}

	for _, r := range m.Reactions {
		if r.Emoji == nil {
			continue
		}
		emoji := r.Emoji.Name
		if emoji == "" {
			emoji = "?"
		}
		v.Reactions = append(v.Reactions, reactionView{Emoji: emoji, Count: r.Count})
	}

	return v
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func render(w http.ResponseWriter, tmpl *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("render template: %v", err)
	}
}

func httpError(w http.ResponseWriter, err error) {
	if errors.Is(err, os.ErrNotExist) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	log.Printf("viewer error: %v", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
