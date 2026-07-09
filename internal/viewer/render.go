package viewer

import (
	"fmt"
	"hash/fnv"
	"html"
	"html/template"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// contentTokenPattern matches the Discord message-content tokens the viewer
// understands: custom emoji, user/role/channel mentions, and bare URLs.
// Everything outside a match is escaped as plain text.
var contentTokenPattern = regexp.MustCompile(
	`<a?:(\w+):\d+>` + // 1: emoji name
		`|<@!?(\d+)>` + // 2: user id
		`|<@&(\d+)>` + // 3: role id
		`|<#(\d+)>` + // 4: channel id
		`|(https?://[^\s<>]+)`, // 5: URL
)

// formatContent renders Discord message content as safe HTML: plain text is
// escaped, mentions are resolved to display names, and URLs are linkified.
// This is intentionally not a full markdown renderer.
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

	var out strings.Builder
	last := 0
	matches := contentTokenPattern.FindAllStringSubmatchIndex(content, -1)
	for _, m := range matches {
		writeEscapedText(&out, content[last:m[0]])
		last = m[1]

		switch {
		case m[2] >= 0: // emoji
			out.WriteString(`<span class="mention emoji">:` + html.EscapeString(content[m[2]:m[3]]) + `:</span>`)
		case m[4] >= 0: // user
			id := content[m[4]:m[5]]
			name, ok := userNames[id]
			if !ok {
				name = "user"
			}
			out.WriteString(`<span class="mention">@` + html.EscapeString(name) + `</span>`)
		case m[6] >= 0: // role
			out.WriteString(`<span class="mention">@role</span>`)
		case m[8] >= 0: // channel
			id := content[m[8]:m[9]]
			name, ok := channelNames[id]
			if !ok {
				name = "deleted-channel"
			}
			out.WriteString(`<span class="mention">#` + html.EscapeString(name) + `</span>`)
		case m[10] >= 0: // URL
			url := content[m[10]:m[11]]
			escaped := html.EscapeString(url)
			out.WriteString(`<a href="` + escaped + `" target="_blank" rel="noopener noreferrer">` + escaped + `</a>`)
		}
	}
	writeEscapedText(&out, content[last:])

	return template.HTML(out.String())
}

func writeEscapedText(out *strings.Builder, text string) {
	if text == "" {
		return
	}
	out.WriteString(strings.ReplaceAll(html.EscapeString(text), "\n", "<br>"))
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
a { color: #00a8fc; text-decoration: none; }
a:hover { text-decoration: underline; }
header {
  padding: 14px 20px; background: #2b2d31; border-bottom: 1px solid #1e1f22;
  position: sticky; top: 0; z-index: 1;
}
header h1 { font-size: 16px; margin: 0; color: #f2f3f5; }
header .crumbs { font-size: 12px; color: #949ba4; margin-top: 4px; }
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
.date-grid { display: flex; flex-wrap: wrap; gap: 6px; }
.date-grid a {
  padding: 6px 10px; background: #2b2d31; border-radius: 6px; font-size: 13px; color: #dbdee1;
}
.date-grid a:hover { background: #3a3c41; text-decoration: none; }
.date-nav { display: flex; justify-content: space-between; margin-bottom: 14px; font-size: 13px; }
.date-nav span.disabled { color: #4a4d53; }
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
.content { white-space: pre-wrap; overflow-wrap: anywhere; line-height: 1.4; }
.mention { background: rgba(88,101,242,.3); color: #c9cdfb; border-radius: 3px; padding: 0 2px; }
.attachments { margin-top: 6px; display: flex; flex-direction: column; gap: 6px; }
.attachments img, .attachments video { max-width: 400px; max-height: 300px; border-radius: 6px; display: block; }
.attachments audio { max-width: 400px; }
.att-file { font-size: 13px; background: #2b2d31; padding: 8px 10px; border-radius: 6px; display: inline-block; }
.att-missing { font-size: 13px; color: #949ba4; background: #2b2d31; padding: 8px 10px; border-radius: 6px; display: inline-block; font-style: italic; }
.embed { border-left: 4px solid #4a4d53; background: #2b2d31; border-radius: 4px; padding: 10px 12px; margin-top: 6px; max-width: 480px; }
.embed .embed-title { font-weight: 600; margin-bottom: 4px; }
.embed .embed-desc { font-size: 13px; color: #c7c9cd; white-space: pre-wrap; }
.embed img { max-width: 100%; max-height: 260px; border-radius: 4px; margin-top: 6px; display: block; }
.reactions { margin-top: 6px; display: flex; gap: 6px; flex-wrap: wrap; }
.reaction { background: #2b2d31; border-radius: 8px; padding: 2px 7px; font-size: 12px; }
.reply { font-size: 12px; color: #949ba4; margin-bottom: 2px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 480px; }
.empty { color: #949ba4; padding: 30px 0; text-align: center; }
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

var datesTemplate = template.Must(template.New("dates").Funcs(funcMap).Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>{{.Channel.Name}} - Discord Archive</title><style>` + baseCSS + `</style></head>
<body>
<header><h1>{{.Channel.Name}}</h1><div class="crumbs"><a href="/g/{{.GuildID}}">&laquo; channels</a></div></header>
<main>
{{if not .Dates}}<p class="empty">アーカイブされたメッセージがありません。</p>{{end}}
<div class="date-grid">
{{range .Dates}}<a href="/g/{{$.GuildID}}/c/{{$.Channel.ID}}/d/{{.}}">{{.}}</a>{{end}}
</div>
</main>
</body></html>
`))

var messagesTemplate = template.Must(template.New("messages").Funcs(funcMap).Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>{{.Channel.Name}} {{.Date}} - Discord Archive</title><style>` + baseCSS + `</style></head>
<body>
<header>
<h1>{{.Channel.Name}} <small style="color:#949ba4;font-weight:400;">{{.Date}}</small></h1>
<div class="crumbs"><a href="/g/{{.GuildID}}">&laquo; channels</a> / <a href="/g/{{.GuildID}}/c/{{.Channel.ID}}">dates</a></div>
</header>
<main>
<div class="date-nav">
{{if .PrevDate}}<a href="/g/{{.GuildID}}/c/{{.Channel.ID}}/d/{{.PrevDate}}">&laquo; {{.PrevDate}}</a>{{else}}<span class="disabled">&laquo;</span>{{end}}
{{if .NextDate}}<a href="/g/{{.GuildID}}/c/{{.Channel.ID}}/d/{{.NextDate}}">{{.NextDate}} &raquo;</a>{{else}}<span class="disabled">&raquo;</span>{{end}}
</div>
{{if not .Messages}}<p class="empty">この日のメッセージはありません。</p>{{end}}
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
        {{else if .IsImage}}<a href="{{.URL}}" target="_blank"><img src="{{.URL}}" alt="{{.Filename}}"></a>
        {{else if .IsVideo}}<video src="{{.URL}}" controls></video>
        {{else if .IsAudio}}<audio src="{{.URL}}" controls></audio>
        {{else}}<a class="att-file" href="{{.URL}}" target="_blank">&#128206; {{.Filename}} ({{formatBytes .Size}})</a>{{end}}
      {{end}}
    </div>{{end}}
    {{if .Embeds}}{{range .Embeds}}<div class="embed" style="border-left-color:{{.Color}}">
      {{if .Title}}<div class="embed-title">{{if .URL}}<a href="{{.URL}}" target="_blank">{{.Title}}</a>{{else}}{{.Title}}{{end}}</div>{{end}}
      {{if .Description}}<div class="embed-desc">{{.Description}}</div>{{end}}
      {{if .ImageURL}}<img src="{{.ImageURL}}" loading="lazy">{{end}}
    </div>{{end}}{{end}}
    {{if .Reactions}}<div class="reactions">{{range .Reactions}}<span class="reaction">{{.Emoji}} {{.Count}}</span>{{end}}</div>{{end}}
  </div>
</div>
{{end}}
</div>
</main>
</body></html>
`))
