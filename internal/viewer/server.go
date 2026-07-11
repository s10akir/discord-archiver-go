package viewer

import (
	"bytes"
	"context"
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
)

type server struct {
	archiveDir      string
	newMessageStore func(root string) messageStore
}

const messagePageSize = 100

// NewHandler builds the viewer's HTTP handler rooted at archiveDir, the same
// -out-dir passed to `dump`/the daemon.
func NewHandler(archiveDir string) (http.Handler, error) {
	info, err := os.Stat(archiveDir)
	if err != nil {
		return nil, fmt.Errorf("open archive directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", archiveDir)
	}

	s := &server{
		archiveDir: archiveDir,
		newMessageStore: func(root string) messageStore {
			return jsonlMessageStore{root: root}
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleGuilds)
	mux.HandleFunc("GET /g/{guild}", s.handleChannels)
	mux.HandleFunc("GET /g/{guild}/c/{channel}", s.handleMessages)
	mux.HandleFunc("GET /g/{guild}/c/{channel}/messages", s.handleMessagePage)
	mux.HandleFunc("GET /g/{guild}/c/{channel}/media", s.handleMedia)
	mux.HandleFunc("GET /g/{guild}/c/{channel}/media/items", s.handleMediaPage)
	mux.HandleFunc("GET /files/{guild}/{rest...}", s.handleFile)
	return mux, nil
}

// Run serves the viewer on addr until ctx is cancelled.
func Run(ctx context.Context, archiveDir, addr string) error {
	handler, err := NewHandler(archiveDir)
	if err != nil {
		return err
	}

	server := &http.Server{Addr: addr, Handler: handler}
	errCh := make(chan error, 1)
	go func() {
		log.Printf("viewer listening on %s (archive: %s)", addr, archiveDir)
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

func (s *server) handleGuilds(w http.ResponseWriter, r *http.Request) {
	guilds, err := listGuilds(s.archiveDir)
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
	Name  string
	Items []container
}

func (s *server) handleChannels(w http.ResponseWriter, r *http.Request) {
	guildID := r.PathValue("guild")
	root := guildRoot(s.archiveDir, guildID)

	containers, names, err := loadContainers(root)
	if err != nil {
		httpError(w, err)
		return
	}

	byParent := make(map[string][]container)
	for _, c := range containers {
		byParent[c.ParentID] = append(byParent[c.ParentID], c)
	}

	var groups []channelGroup
	for parentID, items := range byParent {
		name := names[parentID]
		if name == "" {
			name = "その他"
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		groups = append(groups, channelGroup{Name: name, Items: items})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })

	render(w, channelsTemplate, struct {
		GuildID string
		Groups  []channelGroup
	}{guildID, groups})
}

type messageSection struct {
	Date     string
	Messages []messageView
}

func (s *server) handleMessages(w http.ResponseWriter, r *http.Request) {
	guildID := r.PathValue("guild")
	channelID := r.PathValue("channel")
	root := guildRoot(s.archiveDir, guildID)

	containers, names, err := loadContainers(root)
	if err != nil {
		httpError(w, err)
		return
	}
	channel, ok := findContainer(containers, channelID)
	if !ok {
		channel = container{ID: channelID, Name: channelID}
	}

	page, err := s.newMessageStore(root).Page(channelID, nil, messagePageSize)
	if err != nil {
		httpError(w, err)
		return
	}
	sections := buildMessageSections(root, guildID, channelID, page.Messages, names)

	render(w, messagesTemplate, struct {
		GuildID  string
		Channel  container
		Sections []messageSection
		Cursor   string
		HasMore  bool
	}{guildID, channel, sections, encodeCursor(page.NextCursor), page.HasMore})
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
	_, names, err := loadContainers(root)
	if err != nil {
		log.Printf("viewer error: %v", err)
		writeJSONError(w, "internal error", http.StatusInternalServerError)
		return
	}
	page, err := s.newMessageStore(root).Page(channelID, cursor, messagePageSize)
	if err != nil {
		log.Printf("viewer error: %v", err)
		writeJSONError(w, "internal error", http.StatusInternalServerError)
		return
	}
	sections := buildMessageSections(root, guildID, channelID, page.Messages, names)
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
	AuthorName string
	Timestamp  string
	Attachment *attachmentView
	Embed      *embedView
}

func (s *server) handleMedia(w http.ResponseWriter, r *http.Request) {
	guildID := r.PathValue("guild")
	channelID := r.PathValue("channel")
	root := guildRoot(s.archiveDir, guildID)

	containers, names, err := loadContainers(root)
	if err != nil {
		httpError(w, err)
		return
	}
	channel, ok := findContainer(containers, channelID)
	if !ok {
		channel = container{ID: channelID, Name: channelID}
	}
	page, err := s.newMessageStore(root).MediaPage(channelID, nil, messagePageSize)
	if err != nil {
		httpError(w, err)
		return
	}
	items := buildMediaItems(root, guildID, channelID, page.Messages, names)
	render(w, mediaTemplate, struct {
		GuildID string
		Channel container
		Items   []mediaItem
		Cursor  string
		HasMore bool
	}{guildID, channel, items, encodeCursor(page.NextCursor), page.HasMore})
}

func (s *server) handleMediaPage(w http.ResponseWriter, r *http.Request) {
	guildID := r.PathValue("guild")
	channelID := r.PathValue("channel")
	root := guildRoot(s.archiveDir, guildID)
	cursor, err := decodeCursor(r.URL.Query().Get("before"))
	if err != nil {
		writeJSONError(w, "invalid cursor", http.StatusBadRequest)
		return
	}
	_, names, err := loadContainers(root)
	if err != nil {
		log.Printf("viewer error: %v", err)
		writeJSONError(w, "internal error", http.StatusInternalServerError)
		return
	}
	page, err := s.newMessageStore(root).MediaPage(channelID, cursor, messagePageSize)
	if err != nil {
		log.Printf("viewer error: %v", err)
		writeJSONError(w, "internal error", http.StatusInternalServerError)
		return
	}
	items := buildMediaItems(root, guildID, channelID, page.Messages, names)
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

func buildMediaItems(root, guildID, channelID string, messages []archivedMessage, names map[string]string) []mediaItem {
	var items []mediaItem
	for _, archived := range messages {
		view := buildMessageView(root, guildID, archived.Date, channelID, archived.Message, names)
		for i := range view.Attachments {
			items = append(items, mediaItem{AuthorName: view.AuthorName, Timestamp: view.Timestamp, Attachment: &view.Attachments[i]})
		}
		for i := range view.Embeds {
			items = append(items, mediaItem{AuthorName: view.AuthorName, Timestamp: view.Timestamp, Embed: &view.Embeds[i]})
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

func buildMessageSections(root, guildID, channelID string, messages []archivedMessage, names map[string]string) []messageSection {
	var sections []messageSection
	for _, archived := range messages {
		if len(sections) == 0 || sections[len(sections)-1].Date != archived.Date {
			sections = append(sections, messageSection{Date: archived.Date})
		}
		section := &sections[len(sections)-1]
		section.Messages = append(section.Messages, buildMessageView(root, guildID, archived.Date, channelID, archived.Message, names))
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

func buildMessageView(root, guildID, date, channelID string, m *discordgo.Message, names map[string]string) messageView {
	v := messageView{
		Timestamp: m.Timestamp.Local().Format("2006-01-02 15:04:05"),
		Edited:    m.EditedTimestamp != nil,
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
		if hasLocalAttachment(root, date, channelID, m.ID, a) {
			ct := strings.ToLower(a.ContentType)
			av.Available = true
			av.URL = attachmentURL(guildID, date, channelID, m.ID, a)
			av.IsImage = strings.HasPrefix(ct, "image/")
			av.IsVideo = strings.HasPrefix(ct, "video/")
			av.IsAudio = strings.HasPrefix(ct, "audio/")
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
