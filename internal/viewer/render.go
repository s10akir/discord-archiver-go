package viewer

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"html"
	"html/template"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
)

// discordTokenPattern matches the Discord-specific tokens that need display
// names. URLs are handled by goldmark's linkify extension.
var discordTokenPattern = regexp.MustCompile(
	`<a?:(\w+):\d+>` + // 1: emoji name
		`|<@!?(\d+)>` + // 2: user id
		`|<@&(\d+)>` + // 3: role id
		`|<#(\d+)>`, // 4: channel id
)

var messageMarkdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(goldmarkhtml.WithHardWraps()),
)

type discordReplacement struct {
	placeholder string
	html        string
}

// formatContent renders Discord message Markdown as safe HTML, then restores
// resolved Discord mentions and custom emoji. Raw HTML remains disabled in the
// Markdown renderer, so archived message content cannot inject markup.
func formatContent(content string, mentions []*discordgo.User, channelNames map[string]string) template.HTML {
	if content == "" {
		return ""
	}

	userNames := make(map[string]string, len(mentions))
	for _, u := range mentions {
		if u != nil {
			userNames[u.ID] = u.DisplayName()
		}
	}

	replacements := make([]discordReplacement, 0)
	placeholderPrefix := "DISCORDARCHIVERTOKEN"
	for strings.Contains(content, placeholderPrefix) {
		placeholderPrefix += "X"
	}
	protected := discordTokenPattern.ReplaceAllStringFunc(content, func(token string) string {
		m := discordTokenPattern.FindStringSubmatch(token)
		var rendered string
		switch {
		case m[1] != "": // emoji
			rendered = `<span class="mention emoji">:` + html.EscapeString(m[1]) + `:</span>`
		case m[2] != "": // user
			id := m[2]
			name, ok := userNames[id]
			if !ok {
				name = "user"
			}
			rendered = `<span class="mention">@` + html.EscapeString(name) + `</span>`
		case m[3] != "": // role
			rendered = `<span class="mention">@role</span>`
		case m[4] != "": // channel
			id := m[4]
			name, ok := channelNames[id]
			if !ok {
				name = "deleted-channel"
			}
			rendered = `<span class="mention">#` + html.EscapeString(name) + `</span>`
		}
		placeholder := fmt.Sprintf("%s%dX", placeholderPrefix, len(replacements))
		replacements = append(replacements, discordReplacement{placeholder, rendered})
		return placeholder
	})

	var out bytes.Buffer
	if err := messageMarkdown.Convert([]byte(protected), &out); err != nil {
		return template.HTML(html.EscapeString(content))
	}
	rendered := out.String()
	for _, replacement := range replacements {
		rendered = strings.ReplaceAll(rendered, replacement.placeholder, replacement.html)
	}
	return template.HTML(rendered)
}

// avatarColor derives a stable CSS color from an ID so the same author always
// gets the same initials-avatar color.
func avatarColor(id string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	hue := h.Sum32() % 360
	return fmt.Sprintf("hsl(%d, 55%%, 45%%)", hue)
}

func initials(name string) string {
	r := []rune(strings.TrimSpace(name))
	if len(r) == 0 {
		return "?"
	}
	return strings.ToUpper(string(r[0]))
}

func embedColor(color int) string {
	if color == 0 {
		return "#4a4d53"
	}
	return fmt.Sprintf("#%06x", color&0xffffff)
}

func formatBytes(size int) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := int64(size) / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}

const baseCSS = `
* { box-sizing: border-box; }
body {
  margin: 0; padding: 0;
  background: #313338; color: #dbdee1;
  font-family: "Helvetica Neue", Arial, "Hiragino Kaku Gothic ProN", "Yu Gothic", sans-serif;
}
body.initial-loading { overflow: hidden; }
.initial-loader {
  display: none; position: fixed; inset: 0; z-index: 100;
  background: rgba(30,31,34,.94); align-items: center; justify-content: center;
  flex-direction: column; gap: 12px; color: #dbdee1; font-size: 13px;
}
body.initial-loading .initial-loader { display: flex; }
.loading-spinner {
  width: 34px; height: 34px; border: 4px solid #4a4d53; border-top-color: #5865f2;
  border-radius: 50%; animation: spin .8s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .loading-spinner { animation-duration: 1.8s; } }
a { color: #00a8fc; text-decoration: none; }
a:hover { text-decoration: underline; }
header {
  padding: 14px 20px; background: #2b2d31; border-bottom: 1px solid #1e1f22;
  position: sticky; top: 0; z-index: 1;
}
header h1 { font-size: 16px; margin: 0; color: #f2f3f5; }
header .crumbs { font-size: 12px; color: #949ba4; margin-top: 4px; }
.view-tabs { display: flex; gap: 6px; margin-top: 10px; }
.view-tabs a { color: #b5bac1; padding: 5px 9px; border-radius: 4px; font-size: 13px; }
.view-tabs a:hover { background: #3a3c41; text-decoration: none; }
.view-tabs a.active { color: #fff; background: #404249; }
main { max-width: 900px; margin: 0 auto; padding: 16px 20px 60px; }
.group { margin-bottom: 22px; }
.group h2 { font-size: 12px; text-transform: uppercase; color: #949ba4; letter-spacing: .03em; margin: 0 0 6px; }
.item-list { list-style: none; margin: 0; padding: 0; }
.item-list li { margin-bottom: 2px; }
.item-list a {
  display: block; padding: 7px 10px; border-radius: 6px; color: #dbdee1;
}
.item-list a:hover { background: #3a3c41; text-decoration: none; }
.item-list .badge {
  display: inline-block; font-size: 10px; color: #949ba4; border: 1px solid #4a4d53;
  border-radius: 4px; padding: 0 5px; margin-left: 6px; vertical-align: middle;
}
.date-heading {
  display: flex; align-items: center; gap: 12px; margin: 22px 0 10px;
  color: #949ba4; font-size: 12px; font-weight: 600; scroll-margin-top: 70px;
}
.date-heading::before, .date-heading::after { content: ""; height: 1px; background: #4a4d53; flex: 1; }
.load-status { min-height: 28px; text-align: center; color: #949ba4; font-size: 12px; }
.load-status button { color: #fff; background: #5865f2; border: 0; border-radius: 4px; padding: 5px 10px; cursor: pointer; }
.messages { display: flex; flex-direction: column; gap: 2px; }
.msg { display: flex; gap: 14px; padding: 6px 10px; border-radius: 6px; }
.msg:hover { background: #2e3035; }
.avatar {
  width: 38px; height: 38px; border-radius: 50%; flex: none; position: relative; overflow: hidden;
  display: flex; align-items: center; justify-content: center;
  color: #fff; font-weight: 600; font-size: 15px;
}
.avatar img { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; }
.msg-body { min-width: 0; flex: 1; }
.msg-head { display: flex; align-items: baseline; gap: 8px; }
.author { font-weight: 600; color: #f2f3f5; }
.timestamp { font-size: 12px; color: #949ba4; }
.edited { font-size: 11px; color: #949ba4; }
.content { overflow-wrap: anywhere; line-height: 1.4; }
.content > :first-child { margin-top: 0; }
.content > :last-child { margin-bottom: 0; }
.content p { margin: 0 0 4px; }
.content h1, .content h2, .content h3, .content h4, .content h5, .content h6 { margin: 8px 0 4px; color: #f2f3f5; line-height: 1.2; }
.content h1 { font-size: 1.5em; } .content h2 { font-size: 1.3em; } .content h3 { font-size: 1.15em; }
.content blockquote { margin: 4px 0; padding-left: 10px; border-left: 4px solid #4e5058; color: #b5bac1; }
.content ul, .content ol { margin: 4px 0; padding-left: 24px; }
.content code { background: #2b2d31; border-radius: 3px; padding: 1px 3px; font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: .875em; }
.content pre { margin: 6px 0; padding: 8px; overflow-x: auto; background: #2b2d31; border: 1px solid #1e1f22; border-radius: 4px; }
.content pre code { padding: 0; background: transparent; white-space: pre; }
.content del { color: #949ba4; }
.content table { border-collapse: collapse; margin: 6px 0; }
.content th, .content td { border: 1px solid #4e5058; padding: 3px 7px; }
.mention { background: rgba(88,101,242,.3); color: #c9cdfb; border-radius: 3px; padding: 0 2px; }
.attachments { margin-top: 6px; display: flex; flex-direction: column; gap: 6px; }
.attachments img, .attachments video { width: auto; height: auto; max-width: min(400px, 100%); max-height: 300px; border-radius: 6px; display: block; }
.attachments audio { max-width: 400px; }
.att-file { font-size: 13px; background: #2b2d31; padding: 8px 10px; border-radius: 6px; display: inline-block; }
.att-missing { font-size: 13px; color: #949ba4; background: #2b2d31; padding: 8px 10px; border-radius: 6px; display: inline-block; font-style: italic; }
.embed { border-left: 4px solid #4a4d53; background: #2b2d31; border-radius: 4px; padding: 10px 12px; margin-top: 6px; max-width: 480px; }
.embed .embed-title { font-weight: 600; margin-bottom: 4px; }
.embed .embed-desc { font-size: 13px; color: #c7c9cd; white-space: pre-wrap; }
.embed img { width: auto; height: auto; max-width: 100%; max-height: 260px; border-radius: 4px; margin-top: 6px; display: block; }
.reactions { margin-top: 6px; display: flex; gap: 6px; flex-wrap: wrap; }
.reaction { background: #2b2d31; border-radius: 8px; padding: 2px 7px; font-size: 12px; }
.reply { font-size: 12px; color: #949ba4; margin-bottom: 2px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 480px; }
.empty { color: #949ba4; padding: 30px 0; text-align: center; }
.media-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(220px, 1fr)); gap: 14px; }
.media-card { min-width: 0; overflow: hidden; border-radius: 8px; background: #2b2d31; border: 1px solid #3f4147; }
.media-preview { min-height: 150px; display: flex; align-items: center; justify-content: center; background: #1e1f22; }
.media-preview img, .media-preview video { width: 100%; height: 220px; object-fit: contain; display: block; }
.media-preview audio { width: calc(100% - 20px); }
.media-file { padding: 20px; overflow-wrap: anywhere; text-align: center; }
.media-file .file-size { display: block; color: #949ba4; font-size: 12px; margin-top: 5px; }
.media-embed { align-items: stretch; justify-content: flex-start; flex-direction: column; }
.media-embed-body { padding: 12px; overflow-wrap: anywhere; }
.media-embed-title { font-weight: 600; margin-bottom: 5px; }
.media-embed-desc { color: #c7c9cd; font-size: 13px; white-space: pre-wrap; }
.media-placeholder { color: #949ba4; padding: 32px 12px; text-align: center; }
.media-meta { padding: 9px 11px; font-size: 12px; color: #949ba4; border-top: 1px solid #3f4147; }
.media-meta .media-author { color: #dbdee1; font-weight: 600; margin-right: 6px; }
`

var funcMap = template.FuncMap{
	"avatarColor": avatarColor,
	"initials":    initials,
	"formatBytes": formatBytes,
}

var guildsTemplate = template.Must(template.New("guilds").Funcs(funcMap).Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>Discord Archive</title><style>` + baseCSS + `</style></head>
<body>
<header><h1>Discord Archive</h1></header>
<main>
<div class="group"><h2>Guilds</h2>
<ul class="item-list">
{{range .Guilds}}<li><a href="/g/{{.}}">{{.}}</a></li>{{end}}
</ul>
</div>
</main>
</body></html>
`))

var channelsTemplate = template.Must(template.New("channels").Funcs(funcMap).Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>{{.GuildID}} - Discord Archive</title><style>` + baseCSS + `</style></head>
<body>
<header><h1>Guild {{.GuildID}}</h1><div class="crumbs"><a href="/">&laquo; guilds</a></div></header>
<main>
{{if not .Groups}}<p class="empty">チャンネルが見つかりませんでした。</p>{{end}}
{{range .Groups}}
<div class="group">
<h2>{{.Name}}</h2>
<ul class="item-list">
{{range .Items}}<li><a href="/g/{{$.GuildID}}/c/{{.ID}}">{{.Name}}{{if .IsThread}}<span class="badge">thread</span>{{end}}</a></li>{{end}}
</ul>
</div>
{{end}}
</main>
</body></html>
`))

var messagesTemplate = template.Must(template.New("messages").Funcs(funcMap).Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>{{.Channel.Name}} - Discord Archive</title><style>` + baseCSS + `</style></head>
<body>
<div id="initial-loader" class="initial-loader" role="status" aria-live="polite" aria-hidden="true">
  <div class="loading-spinner" aria-hidden="true"></div>
  <div>メディアを読み込んでいます…</div>
</div>
<script>document.body.classList.add("initial-loading");document.body.setAttribute("aria-busy", "true");document.getElementById("initial-loader").setAttribute("aria-hidden", "false");</script>
<header>
<h1>{{.Channel.Name}}</h1>
<div class="crumbs"><a href="/g/{{.GuildID}}">&laquo; channels</a></div>
<nav class="view-tabs" aria-label="表示切替"><a class="active" href="/g/{{.GuildID}}/c/{{.Channel.ID}}">メッセージ</a><a href="/g/{{.GuildID}}/c/{{.Channel.ID}}/media">メディア</a></nav>
</header>
<main>
{{if not .Sections}}<p class="empty">アーカイブされたメッセージがありません。</p>{{end}}
<div id="load-status" class="load-status"></div>
<div id="older-sentinel" data-cursor="{{.Cursor}}" data-has-more="{{.HasMore}}"></div>
<div id="message-sections">{{template "sections" .Sections}}</div>
<div id="latest"></div>
</main>
<script>
const sentinel = document.getElementById("older-sentinel");
const sectionsRoot = document.getElementById("message-sections");
const loadStatus = document.getElementById("load-status");
const pageHeader = document.querySelector("header");
const pageMain = document.querySelector("main");
pageHeader.inert = true;
pageMain.inert = true;
let loading = false;

function sectionForDate(date) {
  return Array.from(sectionsRoot.children).find(section => section.dataset.date === date);
}

async function loadOlder() {
  if (loading || sentinel.dataset.hasMore !== "true") return;
  loading = true;
  loadStatus.textContent = "過去のメッセージを読み込み中…";
  try {
    const response = await fetch(window.location.pathname + "/messages?before=" + encodeURIComponent(sentinel.dataset.cursor));
    if (!response.ok) throw new Error("request failed: " + response.status);
    const page = await response.json();
    const oldHeight = document.documentElement.scrollHeight;
    const holder = document.createElement("div");
    holder.innerHTML = page.html;
    const newSections = document.createDocumentFragment();
    for (const incoming of Array.from(holder.children)) {
      const existing = sectionForDate(incoming.dataset.date);
      if (existing) {
        const messages = document.createDocumentFragment();
        for (const message of Array.from(incoming.querySelector(".messages").children)) messages.appendChild(message);
        existing.querySelector(".messages").prepend(messages);
      } else {
        newSections.appendChild(incoming);
      }
    }
    sectionsRoot.prepend(newSections);
    sentinel.dataset.cursor = page.next_cursor;
    sentinel.dataset.hasMore = String(page.has_more);
    window.scrollBy(0, document.documentElement.scrollHeight - oldHeight);
    loadStatus.textContent = page.has_more ? "" : "これより古いメッセージはありません。";
    if (!page.has_more) observer.disconnect();
  } catch (error) {
    loadStatus.replaceChildren();
    const retry = document.createElement("button");
    retry.type = "button";
    retry.textContent = "読み込みに失敗しました。再試行";
    retry.addEventListener("click", loadOlder);
    loadStatus.appendChild(retry);
  } finally {
    loading = false;
  }
}

const observer = new IntersectionObserver(entries => {
  if (entries.some(entry => entry.isIntersecting)) loadOlder();
}, { rootMargin: "300px 0px 0px" });
if (sentinel.dataset.hasMore === "true") observer.observe(sentinel);

function waitForInitialImage(image) {
  image.loading = "eager";
  if (image.complete) return Promise.resolve();
  return new Promise(resolve => {
    image.addEventListener("load", resolve, { once: true });
    image.addEventListener("error", resolve, { once: true });
  });
}

function waitForInitialVideo(video) {
  if (video.readyState >= 1) return Promise.resolve();
  return new Promise(resolve => {
    video.addEventListener("loadedmetadata", resolve, { once: true });
    video.addEventListener("error", resolve, { once: true });
  });
}

function nextFrame() {
  return new Promise(resolve => requestAnimationFrame(resolve));
}

async function finishInitialLoading() {
  const media = [
    ...Array.from(sectionsRoot.querySelectorAll("img"), waitForInitialImage),
    ...Array.from(sectionsRoot.querySelectorAll("video"), waitForInitialVideo),
  ];
  if (document.fonts) media.push(document.fonts.ready.catch(() => {}));

  let timeoutID;
  await Promise.race([
    Promise.all(media),
    new Promise(resolve => { timeoutID = setTimeout(resolve, 10000); }),
  ]);
  clearTimeout(timeoutID);
  await nextFrame();
  await nextFrame();
  if (window.location.hash) {
    const target = document.getElementById(window.location.hash.slice(1));
    if (target) target.scrollIntoView();
  } else {
    window.scrollTo({ top: document.documentElement.scrollHeight, behavior: "auto" });
  }
  await nextFrame();
  document.body.classList.remove("initial-loading");
  document.body.setAttribute("aria-busy", "false");
  pageHeader.inert = false;
  pageMain.inert = false;
  document.getElementById("initial-loader").setAttribute("aria-hidden", "true");
}

finishInitialLoading();
</script>
</body></html>
{{define "sections"}}{{range .}}
<section class="date-section" data-date="{{.Date}}">
<div class="date-heading" id="date-{{.Date}}">{{.Date}}</div>
<div class="messages">
{{range .Messages}}
<div class="msg">
  <div class="avatar" style="background:{{avatarColor .AuthorID}}">
    {{initials .AuthorName}}
    {{if .AvatarURL}}<img src="{{.AvatarURL}}" loading="lazy" onerror="this.style.display='none'">{{end}}
  </div>
  <div class="msg-body">
    {{if .ReplySnippet}}<div class="reply">&#8618; {{.ReplySnippet}}</div>{{end}}
    <div class="msg-head">
      <span class="author">{{.AuthorName}}</span>
      <span class="timestamp">{{.Timestamp}}</span>
      {{if .Edited}}<span class="edited">(編集済み)</span>{{end}}
    </div>
    {{if .Content}}<div class="content">{{.Content}}</div>{{end}}
    {{if .Attachments}}<div class="attachments">
      {{range .Attachments}}
        {{if not .Available}}<div class="att-missing">&#9888; asset archive not found: {{.Filename}}</div>
        {{else if .IsImage}}<a href="{{.URL}}" target="_blank"><img src="{{.URL}}" alt="{{.Filename}}"{{if and .Width .Height}} width="{{.Width}}" height="{{.Height}}"{{end}}></a>
        {{else if .IsVideo}}<video src="{{.URL}}" controls preload="metadata"{{if and .Width .Height}} width="{{.Width}}" height="{{.Height}}"{{end}}></video>
        {{else if .IsAudio}}<audio src="{{.URL}}" controls></audio>
        {{else}}<a class="att-file" href="{{.URL}}" target="_blank">&#128206; {{.Filename}} ({{formatBytes .Size}})</a>{{end}}
      {{end}}
    </div>{{end}}
    {{if .Embeds}}{{range .Embeds}}<div class="embed" style="border-left-color:{{.Color}}">
      {{if .Title}}<div class="embed-title">{{if .URL}}<a href="{{.URL}}" target="_blank">{{.Title}}</a>{{else}}{{.Title}}{{end}}</div>{{end}}
      {{if .Description}}<div class="embed-desc">{{.Description}}</div>{{end}}
      {{if .ImageURL}}<img src="{{.ImageURL}}" loading="lazy"{{if and .ImageWidth .ImageHeight}} width="{{.ImageWidth}}" height="{{.ImageHeight}}"{{end}}>{{end}}
    </div>{{end}}{{end}}
    {{if .Reactions}}<div class="reactions">{{range .Reactions}}<span class="reaction">{{.Emoji}} {{.Count}}</span>{{end}}</div>{{end}}
  </div>
</div>
{{end}}
</div>
 </section>
{{end}}{{end}}
`))

var mediaTemplate = template.Must(template.New("media").Funcs(funcMap).Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>{{.Channel.Name}} media - Discord Archive</title><style>` + baseCSS + `</style></head>
<body>
<header>
<h1>{{.Channel.Name}}</h1>
<div class="crumbs"><a href="/g/{{.GuildID}}">&laquo; channels</a></div>
<nav class="view-tabs" aria-label="表示切替"><a href="/g/{{.GuildID}}/c/{{.Channel.ID}}">メッセージ</a><a class="active" href="/g/{{.GuildID}}/c/{{.Channel.ID}}/media">メディア</a></nav>
</header>
<main>
{{if not .Items}}<p id="media-empty" class="empty">アーカイブされたメディアがありません。</p>{{end}}
<div id="media-grid" class="media-grid">{{template "media-items" .Items}}</div>
<div id="media-sentinel" data-cursor="{{.Cursor}}" data-has-more="{{.HasMore}}"></div>
<div id="media-status" class="load-status"></div>
</main>
<script>
const sentinel = document.getElementById("media-sentinel");
const grid = document.getElementById("media-grid");
const status = document.getElementById("media-status");
let loading = false;

async function loadOlderMedia() {
  if (loading || sentinel.dataset.hasMore !== "true") return;
  loading = true;
  status.textContent = "過去のメディアを読み込み中…";
  try {
    const response = await fetch(window.location.pathname + "/items?before=" + encodeURIComponent(sentinel.dataset.cursor));
    if (!response.ok) throw new Error("request failed: " + response.status);
    const page = await response.json();
    grid.insertAdjacentHTML("beforeend", page.html);
    sentinel.dataset.cursor = page.next_cursor;
    sentinel.dataset.hasMore = String(page.has_more);
    status.textContent = page.has_more ? "" : "これより古いメディアはありません。";
    if (!page.has_more) observer.disconnect();
  } catch (error) {
    status.replaceChildren();
    const retry = document.createElement("button");
    retry.type = "button";
    retry.textContent = "読み込みに失敗しました。再試行";
    retry.addEventListener("click", loadOlderMedia);
    status.appendChild(retry);
  } finally {
    loading = false;
  }
}

const observer = new IntersectionObserver(entries => {
  if (entries.some(entry => entry.isIntersecting)) loadOlderMedia();
}, { rootMargin: "0px 0px 300px" });
if (sentinel.dataset.hasMore === "true") observer.observe(sentinel);
</script>
</body></html>
{{define "media-items"}}{{range .}}
<article class="media-card">
  {{with .Attachment}}
    <div class="media-preview">
    {{if not .Available}}<div class="media-placeholder">&#9888; asset archive not found:<br>{{.Filename}}</div>
    {{else if .IsImage}}<a href="{{.URL}}" target="_blank" rel="noopener noreferrer"><img src="{{.URL}}" alt="{{.Filename}}" loading="lazy"{{if and .Width .Height}} width="{{.Width}}" height="{{.Height}}"{{end}}></a>
    {{else if .IsVideo}}<video src="{{.URL}}" controls preload="metadata"{{if and .Width .Height}} width="{{.Width}}" height="{{.Height}}"{{end}}></video>
    {{else if .IsAudio}}<audio src="{{.URL}}" controls preload="metadata"></audio>
    {{else}}<div class="media-file"><a href="{{.URL}}" target="_blank" rel="noopener noreferrer">&#128206; {{.Filename}}</a><span class="file-size">{{formatBytes .Size}}</span></div>{{end}}
    </div>
  {{end}}
  {{with .Embed}}
    <div class="media-preview media-embed" style="border-top:4px solid {{.Color}}">
      {{if .ImageURL}}<img src="{{.ImageURL}}" alt="" loading="lazy"{{if and .ImageWidth .ImageHeight}} width="{{.ImageWidth}}" height="{{.ImageHeight}}"{{end}}>{{end}}
      {{if or .Title .Description}}<div class="media-embed-body">
        {{if .Title}}<div class="media-embed-title">{{if .URL}}<a href="{{.URL}}" target="_blank" rel="noopener noreferrer">{{.Title}}</a>{{else}}{{.Title}}{{end}}</div>{{end}}
        {{if .Description}}<div class="media-embed-desc">{{.Description}}</div>{{end}}
      </div>{{else if not .ImageURL}}<div class="media-placeholder">埋め込み</div>{{end}}
    </div>
  {{end}}
  <div class="media-meta"><span class="media-author">{{.AuthorName}}</span><time>{{.Timestamp}}</time></div>
</article>
{{end}}{{end}}
`))
