package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuildsAPI(t *testing.T) {
	archiveDir := t.TempDir()
	for _, guild := range []string{"guild-b", "guild-a"} {
		if err := os.Mkdir(filepath.Join(archiveDir, "guild_id="+guild), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	handler, err := NewHandler(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/guilds", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("content type = %q", got)
	}
	var response struct {
		Guilds []string `json:"guilds"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Guilds) != 2 || response.Guilds[0] != "guild-a" || response.Guilds[1] != "guild-b" {
		t.Fatalf("guilds = %v", response.Guilds)
	}
}

func TestMediaAPIRejectsUnknownKind(t *testing.T) {
	archiveDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(archiveDir, "guild_id=guild"), 0o755); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/guilds/guild/media/unknown", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestFrontendHandlerServesSPAForDeepLink(t *testing.T) {
	archiveDir := t.TempDir()
	handler, err := NewHandler(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/guilds/example/messages", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `<div id="root"></div>`) {
		t.Fatalf("response = %d %q", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("cache control = %q", got)
	}
}

func TestMessageSectionsAPIUsesEmptyArrays(t *testing.T) {
	sections := messageSectionsAPI([]messageSection{{
		Date:     "2026-07-11",
		Messages: []messageView{{AuthorName: "alice"}},
	}})
	data, err := json.Marshal(sections)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"attachments":[]`, `"embeds":[]`, `"reactions":[]`} {
		if !strings.Contains(string(data), field) {
			t.Fatalf("response %s does not contain %s", data, field)
		}
	}
}
