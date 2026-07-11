package importer

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSyncSendsSnapshotsOnceUntilContentChanges(t *testing.T) {
	root := t.TempDir()
	guildRoot := filepath.Join(root, "guild_id=1")
	writeTestFile(t, filepath.Join(guildRoot, "metadata", "channels.jsonl"), `{"guild_id":"1","channel":{"id":"10","name":"general"}}`+"\n")
	writeTestFile(t, filepath.Join(guildRoot, "messages", "date=2026-07-11", "channel_id=10", "messages.jsonl"), `{"guild_id":"1","channel_id":"10","message":{"id":"100","timestamp":"2026-07-11T00:00:00Z"}}`+"\n")

	var mu sync.Mutex
	requests := make(map[string]int)
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		requests[r.URL.Path]++
		mu.Unlock()
		return &http.Response{StatusCode: http.StatusNoContent, Status: "204 No Content", Body: io.NopCloser(&emptyReader{})}, nil
	})

	runner := New(Config{ArchiveDir: root, WebURL: "http://web", Interval: time.Minute, HTTPTimeout: time.Second})
	runner.client.Transport = transport
	if err := runner.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runner.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests["/api/v1/import/guilds/1/metadata"] != 1 || requests["/api/v1/import/guilds/1/dates/2026-07-11"] != 1 {
		t.Fatalf("requests = %#v", requests)
	}

	// A new runner represents a process restart and must restore the hashes.
	runner = New(Config{ArchiveDir: root, WebURL: "http://web", Interval: time.Minute, HTTPTimeout: time.Second})
	runner.client.Transport = transport
	if err := runner.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests["/api/v1/import/guilds/1/metadata"] != 1 || requests["/api/v1/import/guilds/1/dates/2026-07-11"] != 1 {
		t.Fatalf("requests after restart = %#v", requests)
	}

	writeTestFile(t, filepath.Join(guildRoot, "messages", "date=2026-07-11", "channel_id=10", "messages.jsonl"), `{"guild_id":"1","channel_id":"10","message":{"id":"101","timestamp":"2026-07-11T01:00:00Z"}}`+"\n")
	if err := runner.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests["/api/v1/import/guilds/1/dates/2026-07-11"] != 2 {
		t.Fatalf("requests = %#v", requests)
	}
}

func TestDeletingStateResendsSnapshots(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "guild_id=1", "metadata", "channels.jsonl"), `{"guild_id":"1","channel":{"id":"10"}}`+"\n")
	stateFile := filepath.Join(t.TempDir(), "importer-state.json")
	requests := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: http.StatusNoContent, Status: "204 No Content", Body: io.NopCloser(&emptyReader{})}, nil
	})
	config := Config{ArchiveDir: root, StateFile: stateFile, WebURL: "http://web", Interval: time.Minute, HTTPTimeout: time.Second}

	runner := New(config)
	runner.client.Transport = transport
	if err := runner.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(stateFile); err != nil {
		t.Fatal(err)
	}
	runner = New(config)
	runner.client.Transport = transport
	if err := runner.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

type emptyReader struct{}

func (*emptyReader) Read([]byte) (int, error) { return 0, io.EOF }

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
