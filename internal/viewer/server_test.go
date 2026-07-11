package viewer

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

func TestJSONLMessageStoreMediaPageSkipsTextAndPagesNewestFirst(t *testing.T) {
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
	latest, err := store.MediaPage("channel1", nil, 100)
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

	older, err := store.MediaPage("channel1", latest.NextCursor, 100)
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

func TestMediaPageRendersEachAttachmentAndEmbedAsACard(t *testing.T) {
	archiveDir := t.TempDir()
	root := filepath.Join(archiveDir, "guild_id=guild1")
	writeViewerMetadata(t, root)
	textMessage := testMessage("text", "2026-07-11T08:00:00Z", "text only marker", nil)
	message := testMessage("mixed", "2026-07-11T09:00:00Z", "hidden body marker", nil)
	message.Attachments = []*discordgo.MessageAttachment{
		{ID: "image", Filename: "photo.png", ContentType: "image/png", Size: 12},
		{ID: "file", Filename: "notes.txt", ContentType: "text/plain", Size: 24},
	}
	message.Embeds = []*discordgo.MessageEmbed{{Title: "preview"}, {}}
	writeViewerMessages(t, root, "2026-07-11", textMessage, message)

	handler, err := NewHandler(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/g/guild1/c/channel1/media", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	body := recorder.Body.String()
	if got, want := strings.Count(body, `class="media-card"`), 4; got != want {
		t.Fatalf("media card count = %d, want %d", got, want)
	}
	for _, want := range []string{"photo.png", "notes.txt", "preview", "埋め込み", "alice", "メッセージ", "メディア", "IntersectionObserver"} {
		if !strings.Contains(body, want) {
			t.Errorf("response does not contain %q", want)
		}
	}
	for _, unwanted := range []string{"text only marker", "hidden body marker"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("response unexpectedly contains %q", unwanted)
		}
	}
}

func TestMediaPageWithoutMediaShowsEmptyState(t *testing.T) {
	archiveDir := t.TempDir()
	root := filepath.Join(archiveDir, "guild_id=guild1")
	writeViewerMetadata(t, root)
	writeViewerMessages(t, root, "2026-07-11", testMessage("text", "2026-07-11T08:00:00Z", "text only", nil))

	handler, err := NewHandler(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/g/guild1/c/channel1/media", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !strings.Contains(recorder.Body.String(), "アーカイブされたメディアがありません。") {
		t.Fatal("response does not contain media empty state")
	}
}

func TestMediaItemsRejectsInvalidCursor(t *testing.T) {
	handler, err := NewHandler(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/g/guild1/c/channel1/media/items?before=invalid", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
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
	t.Helper()
	path := filepath.Join(root, "messages", "date="+date, "channel_id=channel1", "messages.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var data []byte
	for _, message := range messages {
		line, err := json.Marshal(messageLine{ChannelID: "channel1", ChannelName: "general", Message: message})
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
