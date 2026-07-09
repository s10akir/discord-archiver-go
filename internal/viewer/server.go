package viewer

import (
	"context"
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
	archiveDir string
}

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

	s := &server{archiveDir: archiveDir}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleGuilds)
	mux.HandleFunc("GET /g/{guild}", s.handleChannels)
	mux.HandleFunc("GET /g/{guild}/c/{channel}", s.handleDates)
	mux.HandleFunc("GET /g/{guild}/c/{channel}/d/{date}", s.handleMessages)
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

func (s *server) handleDates(w http.ResponseWriter, r *http.Request) {
	guildID := r.PathValue("guild")
	channelID := r.PathValue("channel")
	root := guildRoot(s.archiveDir, guildID)

	containers, _, err := loadContainers(root)
	if err != nil {
		httpError(w, err)
		return
	}
	channel, ok := findContainer(containers, channelID)
	if !ok {
		channel = container{ID: channelID, Name: channelID}
	}

	dates, err := listDates(root, channelID)
	if err != nil {
		httpError(w, err)
		return
	}
	// newest first for browsing
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))

	render(w, datesTemplate, struct {
		GuildID string
		Channel container
		Dates   []string
	}{guildID, channel, dates})
}

func (s *server) handleMessages(w http.ResponseWriter, r *http.Request) {
	guildID := r.PathValue("guild")
	channelID := r.PathValue("channel")
	date := r.PathValue("date")
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

	dates, err := listDates(root, channelID)
	if err != nil {
		httpError(w, err)
		return
	}
	prevDate, nextDate := neighborDates(dates, date)

	messages, err := loadMessages(root, date, channelID)
	if err != nil {
		httpError(w, err)
		return
	}

	views := make([]messageView, 0, len(messages))
	for _, m := range messages {
		views = append(views, buildMessageView(root, guildID, date, channelID, m, names))
	}

	render(w, messagesTemplate, struct {
		GuildID  string
		Channel  container
		Date     string
		PrevDate string
		NextDate string
		Messages []messageView
	}{guildID, channel, date, prevDate, nextDate, views})
}

func neighborDates(dates []string, current string) (prev, next string) {
	for i, d := range dates {
		if d == current {
			if i > 0 {
				prev = dates[i-1]
			}
			if i+1 < len(dates) {
				next = dates[i+1]
			}
			return prev, next
		}
	}
	return "", ""
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
		av := attachmentView{Filename: a.Filename, Size: a.Size}
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
		} else if e.Thumbnail != nil {
			ev.ImageURL = e.Thumbnail.URL
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
