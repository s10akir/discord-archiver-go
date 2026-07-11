package web

import (
	"encoding/json"
	"net/http"
	"sort"
)

type apiPage[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
}

type apiNavigation struct {
	GuildID string     `json:"guild_id"`
	Groups  []apiGroup `json:"groups"`
}

type apiGroup struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Uncategorized bool         `json:"uncategorized"`
	Items         []apiChannel `json:"items"`
}

type apiChannel struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	ParentID string       `json:"parent_id,omitempty"`
	IsThread bool         `json:"is_thread"`
	HasLink  bool         `json:"has_link"`
	Threads  []apiChannel `json:"threads,omitempty"`
}

type apiMessageSection struct {
	Date     string       `json:"date"`
	Messages []apiMessage `json:"messages"`
}

type apiMessage struct {
	AuthorID     string           `json:"author_id"`
	AuthorName   string           `json:"author_name"`
	AvatarURL    string           `json:"avatar_url,omitempty"`
	Timestamp    string           `json:"timestamp"`
	Edited       bool             `json:"edited"`
	ContentHTML  string           `json:"content_html,omitempty"`
	ReplySnippet string           `json:"reply_snippet,omitempty"`
	Attachments  []attachmentView `json:"attachments"`
	Embeds       []embedView      `json:"embeds"`
	Reactions    []reactionView   `json:"reactions"`
	ChannelID    string           `json:"channel_id"`
	ChannelName  string           `json:"channel_name"`
}

type apiMediaItem struct {
	AuthorName  string          `json:"author_name"`
	Timestamp   string          `json:"timestamp"`
	ChannelID   string          `json:"channel_id,omitempty"`
	ChannelName string          `json:"channel_name,omitempty"`
	Attachment  *attachmentView `json:"attachment,omitempty"`
	Embed       *embedView      `json:"embed,omitempty"`
}

type apiSearchOptions struct {
	Channels []searchOption `json:"channels"`
	Authors  []searchOption `json:"authors"`
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		writeJSONError(w, "internal error", http.StatusInternalServerError)
	}
}

func (s *server) handleAPIGuilds(w http.ResponseWriter, r *http.Request) {
	guilds, err := s.guilds(r.Context())
	if err != nil {
		writeJSONError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if guilds == nil {
		guilds = []string{}
	}
	writeJSON(w, struct {
		Guilds []string `json:"guilds"`
	}{guilds})
}

func (s *server) handleAPINavigation(w http.ResponseWriter, r *http.Request) {
	guildID := r.PathValue("guild")
	containers, names, err := s.containers(r.Context(), guildID)
	if err != nil {
		writeJSONError(w, "internal error", http.StatusInternalServerError)
		return
	}
	channels := map[string]container{}
	threads := map[string][]container{}
	groups := map[string]*channelGroup{}
	uncategorized := &channelGroup{Name: "その他", Uncategorized: true}
	for _, c := range containers {
		if c.IsThread {
			threads[c.ParentID] = append(threads[c.ParentID], c)
		} else {
			channels[c.ID] = c
		}
		if c.Type == 4 {
			groups[c.ID] = &channelGroup{ID: c.ID, Name: c.Name, Position: c.Position}
		}
	}
	for id := range threads {
		sortThreadsByActivity(threads[id])
	}
	for _, c := range channels {
		if c.Type == 4 {
			continue
		}
		ts := threads[c.ID]
		if !c.CanContainMessages && len(ts) == 0 {
			continue
		}
		item := channelItem{Channel: c, Threads: ts, HasLink: c.CanContainMessages}
		if group := groups[c.ParentID]; group != nil {
			group.Items = append(group.Items, item)
		} else {
			uncategorized.Items = append(uncategorized.Items, item)
		}
		delete(threads, c.ID)
	}
	for parentID, ts := range threads {
		name := names[parentID]
		if name == "" {
			name = "不明なチャンネル"
		}
		uncategorized.Items = append(uncategorized.Items, channelItem{Channel: container{ID: parentID, Name: name}, Threads: ts})
	}
	ordered := []channelGroup{}
	if len(uncategorized.Items) > 0 {
		sortChannelItems(uncategorized.Items)
		ordered = append(ordered, *uncategorized)
	}
	for _, group := range groups {
		if len(group.Items) > 0 {
			sortChannelItems(group.Items)
			ordered = append(ordered, *group)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Uncategorized != ordered[j].Uncategorized {
			return ordered[i].Uncategorized
		}
		if ordered[i].Position != ordered[j].Position {
			return ordered[i].Position < ordered[j].Position
		}
		return ordered[i].ID < ordered[j].ID
	})
	result := apiNavigation{GuildID: guildID, Groups: []apiGroup{}}
	convert := func(c container, linked bool) apiChannel {
		return apiChannel{ID: c.ID, Name: c.Name, ParentID: c.ParentID, IsThread: c.IsThread, HasLink: linked}
	}
	for _, group := range ordered {
		g := apiGroup{ID: group.ID, Name: group.Name, Uncategorized: group.Uncategorized, Items: []apiChannel{}}
		for _, item := range group.Items {
			ch := convert(item.Channel, item.HasLink)
			for _, thread := range item.Threads {
				ch.Threads = append(ch.Threads, convert(thread, true))
			}
			g.Items = append(g.Items, ch)
		}
		result.Groups = append(result.Groups, g)
	}
	writeJSON(w, result)
}

func cursorFromRequest(r *http.Request) (*messageCursor, bool) {
	value := r.URL.Query().Get("before")
	if value == "" {
		return nil, true
	}
	cursor, err := decodeCursor(value)
	return cursor, err == nil
}

func messageSectionsAPI(sections []messageSection) []apiMessageSection {
	result := make([]apiMessageSection, 0, len(sections))
	for _, section := range sections {
		s := apiMessageSection{Date: section.Date, Messages: []apiMessage{}}
		for _, m := range section.Messages {
			attachments := m.Attachments
			if attachments == nil {
				attachments = []attachmentView{}
			}
			embeds := m.Embeds
			if embeds == nil {
				embeds = []embedView{}
			}
			reactions := m.Reactions
			if reactions == nil {
				reactions = []reactionView{}
			}
			s.Messages = append(s.Messages, apiMessage{
				AuthorID: m.AuthorID, AuthorName: m.AuthorName, AvatarURL: m.AvatarURL,
				Timestamp: m.Timestamp, Edited: m.Edited, ContentHTML: string(m.Content),
				ReplySnippet: m.ReplySnippet, Attachments: attachments, Embeds: embeds,
				Reactions: reactions, ChannelID: m.ChannelID, ChannelName: m.ChannelName,
			})
		}
		result = append(result, s)
	}
	return result
}

func (s *server) handleAPIMessages(w http.ResponseWriter, r *http.Request) {
	guildID := r.PathValue("guild")
	channelID := r.URL.Query().Get("channel")
	cursor, ok := cursorFromRequest(r)
	if !ok {
		writeJSONError(w, "invalid cursor", http.StatusBadRequest)
		return
	}
	_, names, err := s.containers(r.Context(), guildID)
	if err != nil {
		writeJSONError(w, "internal error", 500)
		return
	}
	store := s.newMessageStore(guildID)
	var page messagePage
	if channelID == "" {
		page, err = store.AllPage(cursor, messagePageSize)
	} else {
		page, err = store.Page(channelID, cursor, messagePageSize)
	}
	if err != nil {
		writeJSONError(w, "internal error", 500)
		return
	}
	sections := buildMessageSections(guildRoot(s.archiveDir, guildID), guildID, page.Messages, names, channelID == "")
	writeJSON(w, apiPage[apiMessageSection]{messageSectionsAPI(sections), encodeCursor(page.NextCursor), page.HasMore})
}

func parseMediaKind(value string) (mediaKind, bool) {
	for _, k := range mediaKinds {
		if string(k.Kind) == value {
			return k.Kind, true
		}
	}
	return "", false
}

func (s *server) handleAPIMedia(w http.ResponseWriter, r *http.Request) {
	guildID := r.PathValue("guild")
	channelID := r.URL.Query().Get("channel")
	kind, ok := parseMediaKind(r.PathValue("kind"))
	if !ok {
		writeJSONError(w, "unknown media kind", 404)
		return
	}
	cursor, ok := cursorFromRequest(r)
	if !ok {
		writeJSONError(w, "invalid cursor", 400)
		return
	}
	_, names, err := s.containers(r.Context(), guildID)
	if err != nil {
		writeJSONError(w, "internal error", 500)
		return
	}
	store := s.newMessageStore(guildID)
	var page messagePage
	if channelID == "" {
		page, err = store.AllMediaPage(kind, cursor, messagePageSize)
	} else {
		page, err = store.MediaPage(channelID, kind, cursor, messagePageSize)
	}
	if err != nil {
		writeJSONError(w, "internal error", 500)
		return
	}
	views := buildMediaItems(guildRoot(s.archiveDir, guildID), guildID, kind, page.Messages, names, channelID == "")
	items := make([]apiMediaItem, 0, len(views))
	for _, v := range views {
		items = append(items, apiMediaItem{v.AuthorName, v.Timestamp, v.ChannelID, v.ChannelName, v.Attachment, v.Embed})
	}
	writeJSON(w, apiPage[apiMediaItem]{items, encodeCursor(page.NextCursor), page.HasMore})
}

func (s *server) handleAPISearchOptions(w http.ResponseWriter, r *http.Request) {
	channels, authors, _, _, err := searchOptions(r.Context(), s.db, "", "")
	if err != nil {
		writeJSONError(w, "internal error", 500)
		return
	}
	if channels == nil {
		channels = []searchOption{}
	}
	if authors == nil {
		authors = []searchOption{}
	}
	writeJSON(w, apiSearchOptions{channels, authors})
}

func (s *server) handleAPISearch(w http.ResponseWriter, r *http.Request) {
	cursor, ok := cursorFromRequest(r)
	if !ok {
		writeJSONError(w, "invalid cursor", 400)
		return
	}
	filter := parseSearchFilter(r)
	page, err := searchMessages(r.Context(), s.db, filter, cursor, messagePageSize)
	if err != nil {
		writeJSONError(w, "internal error", 500)
		return
	}
	sections := buildMessageSections(guildRoot(s.archiveDir, ""), "", page.Messages, map[string]string{}, true)
	writeJSON(w, apiPage[apiMessageSection]{messageSectionsAPI(sections), encodeCursor(page.NextCursor), page.HasMore})
}
