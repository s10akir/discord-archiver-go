package web

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestFormatContentRendersMarkdownAndDiscordTokens(t *testing.T) {
	got := string(formatContent(
		"**bold** and ~~gone~~\n\n> quote\n\n<@123> in <#456>",
		[]*discordgo.User{{ID: "123", Username: "alice"}},
		map[string]string{"456": "general"},
	))
	for _, want := range []string{
		"<strong>bold</strong>",
		"<del>gone</del>",
		"<blockquote>",
		`<span class="mention">@alice</span>`,
		`<span class="mention">#general</span>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formatContent() = %q, want %q", got, want)
		}
	}
}

func TestFormatContentEscapesRawHTML(t *testing.T) {
	got := string(formatContent(`<script>alert("xss")</script>`, nil, nil))
	if strings.Contains(got, "<script>") || !strings.Contains(got, "<!-- raw HTML omitted -->") {
		t.Fatalf("formatContent() did not safely omit raw HTML: %q", got)
	}
}

func TestFormatContentLinkifiesURL(t *testing.T) {
	got := string(formatContent("https://example.com/path", nil, nil))
	if !strings.Contains(got, `<a href="https://example.com/path">https://example.com/path</a>`) {
		t.Fatalf("formatContent() = %q", got)
	}
}

func TestFormatContentPreservesSingleLineBreak(t *testing.T) {
	got := string(formatContent("first\nsecond", nil, nil))
	if !strings.Contains(got, "first<br>\nsecond") {
		t.Fatalf("formatContent() = %q", got)
	}
}
