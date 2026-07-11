package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestJSONLMessageStorePagesWithoutGapsOrDuplicates(t *testing.T) {
	root := filepath.Join(t.TempDir(), "guild_id=guild1")
	base := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	var firstDate, secondDate []*discordgo.Message
	for i := 0; i < 120; i++ {
		message := testMessage(
			fmt.Sprintf("message-%03d", i),
			base.Add(time.Duration(i)*time.Minute).Format(time.RFC3339),
			fmt.Sprintf("content-%03d", i),
			nil,
		)
		if i < 60 {
			firstDate = append(firstDate, message)
		} else {
			secondDate = append(secondDate, message)
		}
	}
	// Write each file newest-first to prove paging does not depend on JSONL order.
	reverseMessages(firstDate)
	reverseMessages(secondDate)
	writeViewerMessages(t, root, "2026-07-10", firstDate...)
	writeViewerMessages(t, root, "2026-07-11", secondDate...)

	store := jsonlMessageStore{root: root}
	latest, err := store.Page("channel1", nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !latest.HasMore || latest.NextCursor == nil {
		t.Fatal("latest page does not report older messages")
	}
	if got, want := len(latest.Messages), 100; got != want {
		t.Fatalf("latest page length = %d, want %d", got, want)
	}
	if got, want := latest.Messages[0].Message.ID, "message-020"; got != want {
		t.Fatalf("latest oldest ID = %q, want %q", got, want)
	}
	if got, want := latest.Messages[99].Message.ID, "message-119"; got != want {
		t.Fatalf("latest newest ID = %q, want %q", got, want)
	}

	older, err := store.Page("channel1", latest.NextCursor, 100)
	if err != nil {
		t.Fatal(err)
	}
	if older.HasMore || older.NextCursor != nil {
		t.Fatal("oldest page reports another page")
	}
	if got, want := len(older.Messages), 20; got != want {
		t.Fatalf("older page length = %d, want %d", got, want)
	}
	if got, want := older.Messages[0].Message.ID, "message-000"; got != want {
		t.Fatalf("oldest ID = %q, want %q", got, want)
	}
	if got, want := older.Messages[19].Message.ID, "message-019"; got != want {
		t.Fatalf("older newest ID = %q, want %q", got, want)
	}
}

func TestJSONLMessageStoreOrdersEqualTimestampsByID(t *testing.T) {
	root := filepath.Join(t.TempDir(), "guild_id=guild1")
	writeViewerMessages(t, root, "2026-07-10",
		testMessage("b", "2026-07-10T09:00:00Z", "b", nil),
		testMessage("c", "2026-07-10T09:00:00Z", "c", nil),
		testMessage("a", "2026-07-10T09:00:00Z", "a", nil),
	)
	page, err := (jsonlMessageStore{root: root}).Page("channel1", nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{page.Messages[0].Message.ID, page.Messages[1].Message.ID}; got[0] != "b" || got[1] != "c" {
		t.Fatalf("IDs = %v, want [b c]", got)
	}
	older, err := (jsonlMessageStore{root: root}).Page("channel1", page.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := older.Messages[0].Message.ID, "a"; got != want {
		t.Fatalf("older ID = %q, want %q", got, want)
	}
}

func TestJSONLMessageStoreAllPagesAcrossChannelsWithoutGaps(t *testing.T) {
	root := filepath.Join(t.TempDir(), "guild_id=guild1")
	base := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 60; i++ {
		writeViewerMessagesForChannel(t, root, "2026-07-11", "channel1", "general",
			testMessage(fmt.Sprintf("a-%03d", i), base.Add(time.Duration(i*2)*time.Second).Format(time.RFC3339), "a", nil))
		writeViewerMessagesForChannel(t, root, "2026-07-11", "thread1", "topic",
			testMessage(fmt.Sprintf("b-%03d", i), base.Add(time.Duration(i*2+1)*time.Second).Format(time.RFC3339), "b", nil))
	}

	store := jsonlMessageStore{root: root}
	latest, err := store.AllPage(nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(latest.Messages), 100; got != want || !latest.HasMore {
		t.Fatalf("latest page = %d messages, has_more=%v; want %d, true", got, latest.HasMore, want)
	}
	if got, want := latest.Messages[0].Message.ID, "a-010"; got != want {
		t.Fatalf("latest oldest ID = %q, want %q", got, want)
	}
	if got, want := latest.Messages[99].Message.ID, "b-059"; got != want {
		t.Fatalf("latest newest ID = %q, want %q", got, want)
	}

	older, err := store.AllPage(latest.NextCursor, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(older.Messages), 20; got != want || older.HasMore {
		t.Fatalf("older page = %d messages, has_more=%v; want %d, false", got, older.HasMore, want)
	}
	if got, want := older.Messages[0].Message.ID, "a-000"; got != want {
		t.Fatalf("oldest ID = %q, want %q", got, want)
	}
	if got, want := older.Messages[19].Message.ID, "b-009"; got != want {
		t.Fatalf("older newest ID = %q, want %q", got, want)
	}
}

func TestJSONLMessageStoreMediaPageSkipsOtherKindsAndPagesNewestFirst(t *testing.T) {
	root := filepath.Join(t.TempDir(), "guild_id=guild1")
	base := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	messages := make([]*discordgo.Message, 0, 202)
	for i := 0; i < 101; i++ {
		text := testMessage(fmt.Sprintf("text-%03d", i), base.Add(time.Duration(i*2)*time.Second).Format(time.RFC3339), "text only", nil)
		media := testMessage(fmt.Sprintf("media-%03d", i), base.Add(time.Duration(i*2+1)*time.Second).Format(time.RFC3339), "", nil)
		media.Embeds = []*discordgo.MessageEmbed{{Title: fmt.Sprintf("embed-%03d", i)}}
		messages = append(messages, text, media)
	}
	writeViewerMessages(t, root, "2026-07-11", messages...)

	store := jsonlMessageStore{root: root}
	latest, err := store.MediaPage("channel1", mediaEmbed, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(latest.Messages), 100; got != want {
		t.Fatalf("latest media page length = %d, want %d", got, want)
	}
	if got, want := latest.Messages[0].Message.ID, "media-100"; got != want {
		t.Fatalf("first media ID = %q, want %q", got, want)
	}
	if got, want := latest.Messages[99].Message.ID, "media-001"; got != want {
		t.Fatalf("last media ID = %q, want %q", got, want)
	}
	if !latest.HasMore || latest.NextCursor == nil {
		t.Fatal("latest media page does not report older media")
	}

	older, err := store.MediaPage("channel1", mediaEmbed, latest.NextCursor, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(older.Messages), 1; got != want {
		t.Fatalf("older media page length = %d, want %d", got, want)
	}
	if got, want := older.Messages[0].Message.ID, "media-000"; got != want {
		t.Fatalf("older media ID = %q, want %q", got, want)
	}
	if older.HasMore || older.NextCursor != nil {
		t.Fatal("oldest media page reports another page")
	}
}

func TestChannelPageShowsAllDatesInChronologicalOrder(t *testing.T) {
	archiveDir := t.TempDir()
	root := filepath.Join(archiveDir, "guild_id=guild1")
	writeViewerMetadata(t, root)
	writeViewerMessages(t, root, "2026-07-11",
		testMessage("newer", "2026-07-11T12:00:00Z", "newer message", nil),
		testMessage("older-same-day", "2026-07-11T08:00:00Z", "older same day", nil),
	)
	attachment := &discordgo.MessageAttachment{ID: "att1", Filename: "photo.png", ContentType: "image/png", Width: 640, Height: 480}
	oldest := testMessage("oldest", "2026-07-10T09:00:00Z", "oldest message", attachment)
	oldest.Embeds = []*discordgo.MessageEmbed{{
		Title: "preview",
		Image: &discordgo.MessageEmbedImage{URL: "https://example.com/preview.png", Width: 800, Height: 600},
	}}
	writeViewerMessages(t, root, "2026-07-10",
		oldest,
	)
	attachmentPath := filepath.Join(root, "messages", "date=2026-07-10", "channel_id=channel1", "attachments", "oldest", "att1-photo.png")
	if err := os.MkdirAll(filepath.Dir(attachmentPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(attachmentPath, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}

	handler, err := NewHandler(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/g/guild1/c/channel1", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	body := recorder.Body.String()
	for _, want := range []string{
		`id="date-2026-07-10"`,
		`id="date-2026-07-11"`,
		`/files/guild1/messages/date=2026-07-10/channel_id=channel1/attachments/oldest/att1-photo.png`,
		`width="640" height="480"`,
		`width="800" height="600"`,
		`class="initial-loader"`,
		`setTimeout(resolve, 10000)`,
		`window.scrollTo({ top: document.documentElement.scrollHeight`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("response does not contain %q", want)
		}
	}
	assertBefore(t, body, "2026-07-10", "2026-07-11")
	assertBefore(t, body, "oldest message", "older same day")
	assertBefore(t, body, "older same day", "newer message")
}

func TestChannelListUsesDiscordOrderAndNestsThreads(t *testing.T) {
	archiveDir := t.TempDir()
	root := filepath.Join(archiveDir, "guild_id=guild1")
	metadataDir := filepath.Join(root, "metadata")
	if err := os.MkdirAll(metadataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	channels := []*discordgo.Channel{
		{ID: "cat-later", Name: "Alpha category", Type: discordgo.ChannelTypeGuildCategory, Position: 20},
		{ID: "cat-first", Name: "Zulu category", Type: discordgo.ChannelTypeGuildCategory, Position: 10},
		{ID: "text-later", Name: "alpha-channel", Type: discordgo.ChannelTypeGuildText, ParentID: "cat-first", Position: 8},
		{ID: "text-first", Name: "zulu-channel", Type: discordgo.ChannelTypeGuildText, ParentID: "cat-first", Position: 2},
		{ID: "forum", Name: "forum-parent", Type: discordgo.ChannelTypeGuildForum, ParentID: "cat-later", Position: 1},
	}
	var channelData []byte
	for _, channel := range channels {
		line, err := json.Marshal(channelMetaLine{Channel: channel})
		if err != nil {
			t.Fatal(err)
		}
		channelData = append(channelData, append(line, '\n')...)
	}
	if err := os.WriteFile(filepath.Join(metadataDir, "channels.jsonl"), channelData, 0o644); err != nil {
		t.Fatal(err)
	}

	threads := []threadMetaLine{
		{Thread: &discordgo.Channel{ID: "100", Name: "older-thread", Type: discordgo.ChannelTypeGuildPublicThread, ParentID: "forum", LastMessageID: "200"}},
		{Thread: &discordgo.Channel{ID: "300", Name: "newer-thread", Type: discordgo.ChannelTypeGuildPublicThread, ParentID: "forum", LastMessageID: "400"}},
	}
	var threadData []byte
	for _, thread := range threads {
		line, err := json.Marshal(thread)
		if err != nil {
			t.Fatal(err)
		}
		threadData = append(threadData, append(line, '\n')...)
	}
	if err := os.WriteFile(filepath.Join(metadataDir, "threads.jsonl"), threadData, 0o644); err != nil {
		t.Fatal(err)
	}

	handler, err := NewHandler(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/g/guild1", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	body := recorder.Body.String()
	assertBefore := func(first, second string) {
		t.Helper()
		firstIndex, secondIndex := strings.Index(body, first), strings.Index(body, second)
		if firstIndex < 0 || secondIndex < 0 || firstIndex >= secondIndex {
			t.Fatalf("want %q before %q in channel list", first, second)
		}
	}
	assertBefore("Zulu category", "Alpha category")
	assertBefore("zulu-channel", "alpha-channel")
	assertBefore("newer-thread", "older-thread")
	if strings.Contains(body, `/g/guild1/c/forum`) {
		t.Fatal("forum parent unexpectedly rendered as a link")
	}
	if !strings.Contains(body, `<span class="channel-parent">forum-parent</span>`) ||
		!strings.Contains(body, `<details class="thread-accordion"><summary>`) ||
		!strings.Contains(body, `<ul class="thread-list">`) {
		t.Fatal("forum parent and collapsed nested thread list were not rendered")
	}
	if strings.Contains(body, `<details class="thread-accordion" open`) {
		t.Fatal("thread accordion unexpectedly starts open")
	}
}

func TestChannelPageInitiallyRendersOnlyLatestHundredMessages(t *testing.T) {
	archiveDir := t.TempDir()
	root := filepath.Join(archiveDir, "guild_id=guild1")
	writeViewerMetadata(t, root)
	base := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	messages := make([]*discordgo.Message, 0, 101)
	for i := 0; i < 101; i++ {
		messages = append(messages, testMessage(
			fmt.Sprintf("id-%03d", i),
			base.Add(time.Duration(i)*time.Second).Format(time.RFC3339),
			fmt.Sprintf("initial-content-%03d", i),
			nil,
		))
	}
	writeViewerMessages(t, root, "2026-07-11", messages...)

	handler, err := NewHandler(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/g/guild1/c/channel1", nil))
	body := recorder.Body.String()
	if strings.Contains(body, "initial-content-000") {
		t.Fatal("initial page contains the oldest message beyond the page limit")
	}
	for _, want := range []string{"initial-content-001", "initial-content-100", `data-has-more="true"`, "IntersectionObserver"} {
		if !strings.Contains(body, want) {
			t.Errorf("response does not contain %q", want)
		}
	}
}

func TestChannelPageWithoutMessagesShowsEmptyState(t *testing.T) {
	archiveDir := t.TempDir()
	root := filepath.Join(archiveDir, "guild_id=guild1")
	writeViewerMetadata(t, root)

	handler, err := NewHandler(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/g/guild1/c/channel1", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !strings.Contains(recorder.Body.String(), "アーカイブされたメッセージがありません。") {
		t.Fatal("response does not contain empty state")
	}
}

func TestAllChannelsPageShowsSourcesAndChannelSpecificLinks(t *testing.T) {
	archiveDir := t.TempDir()
	root := filepath.Join(archiveDir, "guild_id=guild1")
	writeViewerMetadata(t, root)
	writeViewerMessagesForChannel(t, root, "2026-07-11", "channel1", "general",
		testMessage("first", "2026-07-11T08:00:00Z", "general marker", nil))
	attachment := &discordgo.MessageAttachment{ID: "image", Filename: "photo.png", ContentType: "image/png"}
	writeViewerMessagesForChannel(t, root, "2026-07-11", "thread1", "topic",
		testMessage("second", "2026-07-11T09:00:00Z", "thread marker", attachment))

	handler, err := NewHandler(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		path string
		want []string
	}{
		{"/g/guild1", []string{`href="/g/guild1/all"`, "全チャンネル"}},
		{"/g/guild1/all", []string{"general marker", "thread marker", `href="/g/guild1/c/channel1"`, "#general", `href="/g/guild1/c/thread1"`, "#topic", `href="/g/guild1/all/images"`}},
		{"/g/guild1/all/images", []string{"photo.png", `href="/g/guild1/c/thread1/images"`, "#topic"}},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tt.path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", tt.path, recorder.Code, http.StatusOK)
		}
		for _, want := range tt.want {
			if !strings.Contains(recorder.Body.String(), want) {
				t.Errorf("%s response does not contain %q", tt.path, want)
			}
		}
	}
}

func TestMediaKindPagesRenderOnlyMatchingCards(t *testing.T) {
	archiveDir := t.TempDir()
	root := filepath.Join(archiveDir, "guild_id=guild1")
	writeViewerMetadata(t, root)
	textMessage := testMessage("text", "2026-07-11T08:00:00Z", "text only marker", nil)
	message := testMessage("mixed", "2026-07-11T09:00:00Z", "hidden body marker", nil)
	message.Attachments = []*discordgo.MessageAttachment{
		{ID: "image", Filename: "photo.png", ContentType: "image/png", Size: 12},
		{ID: "video", Filename: "clip.mp4", ContentType: "video/mp4", Size: 18},
		{ID: "audio", Filename: "voice.ogg", ContentType: "audio/ogg", Size: 20},
		{ID: "file", Filename: "notes.txt", ContentType: "text/plain", Size: 24},
	}
	message.Embeds = []*discordgo.MessageEmbed{{Title: "preview"}, {}}
	writeViewerMessages(t, root, "2026-07-11", textMessage, message)

	handler, err := NewHandler(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path      string
		want      []string
		cardCount int
	}{
		{"images", []string{"photo.png"}, 1},
		{"videos", []string{"clip.mp4"}, 1},
		{"audio", []string{"voice.ogg"}, 1},
		{"files", []string{"notes.txt"}, 1},
		{"embeds", []string{"preview", "埋め込み"}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/g/guild1/c/channel1/"+tt.path, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
			}
			body := recorder.Body.String()
			if got := strings.Count(body, `class="media-card"`); got != tt.cardCount {
				t.Fatalf("media card count = %d, want %d", got, tt.cardCount)
			}
			for _, want := range append(tt.want, "alice", "メッセージ", "画像", "動画", "音声", "ファイル", "埋め込み", "IntersectionObserver") {
				if !strings.Contains(body, want) {
					t.Errorf("response does not contain %q", want)
				}
			}
			for _, unwanted := range []string{"text only marker", "hidden body marker"} {
				if strings.Contains(body, unwanted) {
					t.Errorf("response unexpectedly contains %q", unwanted)
				}
			}
		})
	}
}

func TestMediaKindPageWithoutMatchingItemsShowsEmptyState(t *testing.T) {
	archiveDir := t.TempDir()
	root := filepath.Join(archiveDir, "guild_id=guild1")
	writeViewerMetadata(t, root)
	writeViewerMessages(t, root, "2026-07-11", testMessage("text", "2026-07-11T08:00:00Z", "text only", nil))

	handler, err := NewHandler(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/g/guild1/c/channel1/images", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !strings.Contains(recorder.Body.String(), "アーカイブされた画像がありません。") {
		t.Fatal("response does not contain media empty state")
	}
}

func TestMediaKindItemsRejectInvalidCursor(t *testing.T) {
	handler, err := NewHandler(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/g/guild1/c/channel1/images/items?before=invalid", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestLegacyMediaRoutesAreNotAvailable(t *testing.T) {
	handler, err := NewHandler(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/g/guild1/c/channel1/media", "/g/guild1/c/channel1/media/items"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want %d", path, recorder.Code, http.StatusNotFound)
		}
	}
}

func TestAttachmentMediaKindUsesMIMEThenExtensionFallback(t *testing.T) {
	tests := []struct {
		name       string
		attachment *discordgo.MessageAttachment
		want       mediaKind
	}{
		{"image MIME", &discordgo.MessageAttachment{Filename: "asset.bin", ContentType: "image/png"}, mediaImage},
		{"video MIME parameters", &discordgo.MessageAttachment{Filename: "asset.bin", ContentType: "video/mp4; charset=binary"}, mediaVideo},
		{"audio extension", &discordgo.MessageAttachment{Filename: "voice.ogg"}, mediaAudio},
		{"generic MIME extension", &discordgo.MessageAttachment{Filename: "photo.png", ContentType: "application/octet-stream"}, mediaImage},
		{"MIME wins", &discordgo.MessageAttachment{Filename: "photo.png", ContentType: "text/plain"}, mediaFile},
		{"unknown", &discordgo.MessageAttachment{Filename: "archive.unknown"}, mediaFile},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := attachmentMediaKind(tt.attachment); got != tt.want {
				t.Fatalf("attachmentMediaKind() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDateURLIsNotAvailable(t *testing.T) {
	handler, err := NewHandler(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/g/guild1/c/channel1/d/2026-07-10", nil))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestMessagePageRejectsInvalidCursor(t *testing.T) {
	handler, err := NewHandler(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/g/guild1/c/channel1/messages?before=invalid", nil))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestMessagePageReturnsRenderedOlderMessages(t *testing.T) {
	archiveDir := t.TempDir()
	root := filepath.Join(archiveDir, "guild_id=guild1")
	writeViewerMetadata(t, root)
	writeViewerMessages(t, root, "2026-07-10",
		testMessage("old", "2026-07-10T09:00:00Z", "older page content", nil),
	)
	cursor := encodeCursor(&messageCursor{Date: "2026-07-10", Timestamp: "2026-07-10T10:00:00Z", ID: "cursor"})

	handler, err := NewHandler(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/g/guild1/c/channel1/messages?before="+cursor, nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var response struct {
		HTML       string `json:"html"`
		NextCursor string `json:"next_cursor"`
		HasMore    bool   `json:"has_more"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(response.HTML, `data-date="2026-07-10"`) || !strings.Contains(response.HTML, "older page content") {
		t.Fatalf("HTML = %q, want date section and message", response.HTML)
	}
	if response.HasMore || response.NextCursor != "" {
		t.Fatalf("response reports unexpected older page: %+v", response)
	}
}

func TestSearchResultUsesMessageGuildForAttachmentPath(t *testing.T) {
	archiveDir := t.TempDir()
	attachment := &discordgo.MessageAttachment{ID: "attachment1", Filename: "image.png", ContentType: "image/png", Size: 5}
	path := filepath.Join(archiveDir, "guild_id=guild1", "messages", "date=2026-07-11", "channel_id=channel1", "attachments", "message1", "attachment1-image.png")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}

	sections := buildMessageSections(
		guildRoot(archiveDir, ""), "",
		[]archivedMessage{{GuildID: "guild1", Date: "2026-07-11", ChannelID: "channel1", Message: testMessage("message1", "2026-07-11T00:00:00Z", "", attachment)}},
		map[string]string{"channel1": "general"}, true,
	)
	got := sections[0].Messages[0]
	if !got.Attachments[0].Available {
		t.Fatal("attachment reported unavailable")
	}
	if got.Attachments[0].URL != "/files/guild1/messages/date=2026-07-11/channel_id=channel1/attachments/message1/attachment1-image.png" {
		t.Fatalf("attachment URL = %q", got.Attachments[0].URL)
	}
}

func writeViewerMetadata(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, "metadata", "channels.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(channelMetaLine{Channel: &discordgo.Channel{
		ID: "channel1", Name: "general", Type: discordgo.ChannelTypeGuildText,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeViewerMessages(t *testing.T, root, date string, messages ...*discordgo.Message) {
	writeViewerMessagesForChannel(t, root, date, "channel1", "general", messages...)
}

func writeViewerMessagesForChannel(t *testing.T, root, date, channelID, channelName string, messages ...*discordgo.Message) {
	t.Helper()
	path := filepath.Join(root, "messages", "date="+date, "channel_id="+channelID, "messages.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, message := range messages {
		line, err := json.Marshal(messageLine{ChannelID: channelID, ChannelName: channelName, Message: message})
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, line...)
		data = append(data, '\n')
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func testMessage(id, timestamp, content string, attachment *discordgo.MessageAttachment) *discordgo.Message {
	parsed, _ := time.Parse(time.RFC3339, timestamp)
	message := &discordgo.Message{
		ID: id, Timestamp: parsed, Content: content,
		Author: &discordgo.User{ID: "user1", Username: "alice"},
	}
	if attachment != nil {
		message.Attachments = []*discordgo.MessageAttachment{attachment}
	}
	return message
}

func reverseMessages(messages []*discordgo.Message) {
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
}

func assertBefore(t *testing.T, text, first, second string) {
	t.Helper()
	firstIndex := strings.Index(text, first)
	secondIndex := strings.Index(text, second)
	if firstIndex < 0 || secondIndex < 0 || firstIndex >= secondIndex {
		t.Errorf("%q does not appear before %q", first, second)
	}
}
