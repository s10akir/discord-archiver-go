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
	mux.HandleFunc("GET /api/v1/guilds", s.handleAPIGuilds)
	mux.HandleFunc("GET /api/v1/guilds/{guild}/navigation", s.handleAPINavigation)
	mux.HandleFunc("GET /api/v1/guilds/{guild}/messages", s.handleAPIMessages)
	mux.HandleFunc("GET /api/v1/guilds/{guild}/media/{kind}", s.handleAPIMedia)
	if db != nil {
		mux.HandleFunc("GET /api/v1/search/options", s.handleAPISearchOptions)
		mux.HandleFunc("GET /api/v1/search/messages", s.handleAPISearch)
	}
	mux.HandleFunc("GET /files/{guild}/{rest...}", s.handleFile)
	if db != nil {
		mux.HandleFunc("PUT /api/v1/import/guilds/{guild}/metadata", s.handleImportMetadata)
		mux.HandleFunc("PUT /api/v1/import/guilds/{guild}/dates/{date}", s.handleImportDate)
	}
	mux.Handle("GET /", frontendHandler())
	return mux, nil
}

var searchTemplate = template.Must(template.New("search").Parse(`<!doctype html><html lang="ja"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>アーカイブ検索</title><style>` + baseCSS + `
.search-form{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:10px;background:#2b2d31;padding:14px;border-radius:8px;margin-bottom:18px}.search-field{font-size:12px;color:#b5bac1;position:relative}.search-field>input,.search-field>select{display:block;width:100%;margin-top:4px;padding:8px;background:#1e1f22;color:#fff;border:1px solid #4a4d53;border-radius:4px}.search-submit{align-self:end;padding:9px;background:#5865f2;color:white;border:0;border-radius:4px}.combo-input-wrap{display:flex;gap:4px}.combo-input-wrap input{min-width:0;flex:1}.combo-clear{border:1px solid #4a4d53;background:#383a40;color:#b5bac1;border-radius:4px}.combo-options{position:absolute;z-index:5;top:100%;left:0;right:0;max-height:260px;overflow:auto;background:#1e1f22;border:1px solid #4a4d53;border-radius:4px;box-shadow:0 8px 20px #0008}.combo-options[hidden]{display:none}.combo-option{display:block;width:100%;padding:8px;text-align:left;color:#dbdee1;background:none;border:0}.combo-option:hover,.combo-option.active{background:#404249}.combo-empty{padding:8px;color:#949ba4}</style></head><body><header><h1>アーカイブ検索</h1><div class="crumbs"><a href="/">アーカイブ</a></div></header><main><form class="search-form" method="get">
<label class="search-field">キーワード<input name="q" value="{{.Query}}"></label>
<div class="search-field combo" data-combobox><span>チャンネル / スレッド</span><div class="combo-input-wrap"><input class="combo-input" value="{{.ChannelLabel}}" autocomplete="off" role="combobox" aria-autocomplete="list" aria-expanded="false"><button class="combo-clear" type="button" aria-label="チャンネル選択を解除">×</button></div><input class="combo-value" type="hidden" name="channel" value="{{.Channel}}"><div class="combo-options" role="listbox" hidden>{{range .Channels}}<button type="button" class="combo-option" role="option" data-value="{{.Value}}" data-label="{{.Label}}" aria-selected="{{.Selected}}">{{.Label}}</button>{{end}}<div class="combo-empty" hidden>候補がありません</div></div></div>
<div class="search-field combo" data-combobox><span>投稿者</span><div class="combo-input-wrap"><input class="combo-input" value="{{.AuthorLabel}}" autocomplete="off" role="combobox" aria-autocomplete="list" aria-expanded="false"><button class="combo-clear" type="button" aria-label="投稿者選択を解除">×</button></div><input class="combo-value" type="hidden" name="author" value="{{.Author}}"><div class="combo-options" role="listbox" hidden>{{range .Authors}}<button type="button" class="combo-option" role="option" data-value="{{.Value}}" data-label="{{.Label}}" aria-selected="{{.Selected}}">{{.Label}}</button>{{end}}<div class="combo-empty" hidden>候補がありません</div></div></div>
<label class="search-field">開始日時<input type="datetime-local" name="from" value="{{.From}}"></label><label class="search-field">終了日時<input type="datetime-local" name="to" value="{{.To}}"></label>
<label class="search-field">添付<select name="attachment"><option value=""{{if eq .Attachment ""}} selected{{end}}>指定なし</option><option value="yes"{{if eq .Attachment "yes"}} selected{{end}}>あり</option><option value="no"{{if eq .Attachment "no"}} selected{{end}}>なし</option></select></label>
<label class="search-field">メディア<select name="media"><option value=""{{if eq .Media ""}} selected{{end}}>指定なし</option><option value="image"{{if eq .Media "image"}} selected{{end}}>画像</option><option value="video"{{if eq .Media "video"}} selected{{end}}>動画</option><option value="audio"{{if eq .Media "audio"}} selected{{end}}>音声</option><option value="embed"{{if eq .Media "embed"}} selected{{end}}>埋め込み</option></select></label>
<label class="search-field">埋め込み<select name="embed"><option value=""{{if eq .Embed ""}} selected{{end}}>指定なし</option><option value="yes"{{if eq .Embed "yes"}} selected{{end}}>あり</option><option value="no"{{if eq .Embed "no"}} selected{{end}}>なし</option></select></label><button class="search-submit">検索</button></form>
<div id="search-results">{{.Results}}</div><div id="search-sentinel" data-cursor="{{.Cursor}}" data-has-more="{{.HasMore}}"></div><div id="search-status" class="load-status"></div></main><script>
for(const combo of document.querySelectorAll("[data-combobox]")){const input=combo.querySelector(".combo-input"),value=combo.querySelector(".combo-value"),options=combo.querySelector(".combo-options"),items=Array.from(combo.querySelectorAll(".combo-option")),empty=combo.querySelector(".combo-empty");let active=-1;const open=()=>{options.hidden=false;input.setAttribute("aria-expanded","true")};const close=()=>{options.hidden=true;input.setAttribute("aria-expanded","false");active=-1;items.forEach(item=>item.classList.remove("active"))};const filter=()=>{const query=input.value.trim().toLocaleLowerCase();let visible=0;for(const item of items){const show=item.dataset.label.toLocaleLowerCase().includes(query);item.hidden=!show;if(show)visible++}empty.hidden=visible!==0;active=-1;open()};const choose=item=>{input.value=item.dataset.label;value.value=item.dataset.value;items.forEach(option=>option.setAttribute("aria-selected",String(option===item)));close()};input.addEventListener("focus",filter);input.addEventListener("input",()=>{value.value="";items.forEach(item=>item.setAttribute("aria-selected","false"));filter()});input.addEventListener("keydown",event=>{const visible=items.filter(item=>!item.hidden);if(event.key==="ArrowDown"||event.key==="ArrowUp"){event.preventDefault();open();const step=event.key==="ArrowDown"?1:-1;active=(active+step+visible.length)%visible.length;items.forEach(item=>item.classList.remove("active"));if(visible[active]){visible[active].classList.add("active");visible[active].scrollIntoView({block:"nearest"})}}else if(event.key==="Enter"&&active>=0){event.preventDefault();choose(visible[active])}else if(event.key==="Escape"){close()}});for(const item of items)item.addEventListener("click",()=>choose(item));combo.querySelector(".combo-clear").addEventListener("click",()=>{input.value="";value.value="";items.forEach(item=>item.setAttribute("aria-selected","false"));filter();input.focus()});document.addEventListener("click",event=>{if(!combo.contains(event.target))close()})}
const sentinel=document.getElementById("search-sentinel"),results=document.getElementById("search-results"),status=document.getElementById("search-status");let loading=false;
async function loadOlder(){if(loading||sentinel.dataset.hasMore!=="true")return;loading=true;status.textContent="過去の検索結果を読み込み中…";try{const params=new URLSearchParams(new FormData(document.querySelector(".search-form")));params.set("before",sentinel.dataset.cursor);const response=await fetch("/search/messages?"+params);if(!response.ok)throw new Error(response.status);const page=await response.json();const holder=document.createElement("div");holder.innerHTML=page.html;while(holder.firstChild)results.appendChild(holder.firstChild);sentinel.dataset.cursor=page.next_cursor;sentinel.dataset.hasMore=String(page.has_more);status.textContent=page.has_more?"":"これより古い検索結果はありません。";if(!page.has_more)observer.disconnect()}catch(error){status.textContent="読み込みに失敗しました。スクロールすると再試行します。"}finally{loading=false}}
const observer=new IntersectionObserver(entries=>{if(entries.some(entry=>entry.isIntersecting))loadOlder()},{rootMargin:"300px 0px 0px"});if(sentinel.dataset.hasMore==="true")observer.observe(sentinel);
</script></body></html>`))

type searchPageView struct {
	Query, Channel, ChannelLabel, Author, AuthorLabel, From, To string
	Attachment, Media, Embed                                    string
	Channels, Authors                                           []searchOption
	Results                                                     template.HTML
	Cursor                                                      string
	HasMore                                                     bool
}

func parseSearchFilter(r *http.Request) searchFilter {
	q := r.URL.Query()
	parseTime := func(value string) time.Time { parsed, _ := time.Parse("2006-01-02T15:04", value); return parsed }
	oneOf := func(value string, allowed ...string) string {
		for _, candidate := range allowed {
			if value == candidate {
				return value
			}
		}
		return ""
	}
	return searchFilter{ChannelID: q.Get("channel"), Author: q.Get("author"), Query: q.Get("q"), Media: oneOf(q.Get("media"), "image", "video", "audio", "embed"), Attachment: oneOf(q.Get("attachment"), "yes", "no"), Embed: oneOf(q.Get("embed"), "yes", "no"), From: parseTime(q.Get("from")), To: parseTime(q.Get("to"))}
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := parseSearchFilter(r)
	channels, authors, channelFound, authorFound, err := searchOptions(r.Context(), s.db, filter.ChannelID, filter.Author)
	if err != nil {
		httpError(w, err)
		return
	}
	if !channelFound {
		filter.ChannelID = ""
	}
	if !authorFound {
		filter.Author = ""
	}
	page, err := searchMessages(r.Context(), s.db, filter, nil, messagePageSize)
	if err != nil {
		httpError(w, err)
		return
	}
	names := map[string]string{}
	var results bytes.Buffer
	if len(page.Messages) == 0 {
		results.WriteString(`<p class="empty">条件に一致するメッセージはありません。</p>`)
	} else {
		sections := buildMessageSections(guildRoot(s.archiveDir, ""), "", page.Messages, names, true)
		if err := messagesTemplate.ExecuteTemplate(&results, "sections", sections); err != nil {
			httpError(w, err)
			return
		}
	}
	selectedLabel := func(options []searchOption) string {
		for _, option := range options {
			if option.Selected {
				return option.Label
			}
		}
		return ""
	}
	data := searchPageView{Query: q.Get("q"), Channel: filter.ChannelID, ChannelLabel: selectedLabel(channels), Author: filter.Author, AuthorLabel: selectedLabel(authors), From: q.Get("from"), To: q.Get("to"), Attachment: filter.Attachment, Media: filter.Media, Embed: filter.Embed, Channels: channels, Authors: authors, Results: template.HTML(results.String()), Cursor: encodeCursor(page.NextCursor), HasMore: page.HasMore}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := searchTemplate.Execute(w, data); err != nil {
		log.Printf("render search: %v", err)
	}
}

func (s *server) handleSearchPage(w http.ResponseWriter, r *http.Request) {
	cursor, err := decodeCursor(r.URL.Query().Get("before"))
	if err != nil {
		writeJSONError(w, "invalid cursor", http.StatusBadRequest)
		return
	}
	filter := parseSearchFilter(r)
	page, err := searchMessages(r.Context(), s.db, filter, cursor, messagePageSize)
	if err != nil {
		writeJSONError(w, "internal error", http.StatusInternalServerError)
		return
	}
	names := map[string]string{}
	sections := buildMessageSections(guildRoot(s.archiveDir, ""), "", page.Messages, names, true)
	var fragment bytes.Buffer
	if err := messagesTemplate.ExecuteTemplate(&fragment, "sections", sections); err != nil {
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
		messageGuildID := guildID
		messageRoot := root
		if archived.GuildID != "" {
			messageGuildID = archived.GuildID
			messageRoot = guildRoot(filepath.Dir(root), messageGuildID)
		}
		view := buildMessageView(messageRoot, messageGuildID, archived, names)
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
		messageGuildID := guildID
		messageRoot := root
		if archived.GuildID != "" {
			messageGuildID = archived.GuildID
			messageRoot = guildRoot(filepath.Dir(root), messageGuildID)
		}
		view := buildMessageView(messageRoot, messageGuildID, archived, names)
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
	URL       string `json:"url,omitempty"`
	Filename  string `json:"filename"`
	Size      int    `json:"size"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	IsImage   bool   `json:"is_image"`
	IsVideo   bool   `json:"is_video"`
	IsAudio   bool   `json:"is_audio"`
	Available bool   `json:"available"`
}

type embedView struct {
	Title       string `json:"title,omitempty"`
	URL         string `json:"url,omitempty"`
	Description string `json:"description,omitempty"`
	ImageURL    string `json:"image_url,omitempty"`
	ImageWidth  int    `json:"image_width,omitempty"`
	ImageHeight int    `json:"image_height,omitempty"`
	Color       string `json:"color"`
}

type reactionView struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
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
