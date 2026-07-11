// Package importer synchronizes committed archive JSONL snapshots to the web API.
package importer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Config struct {
	ArchiveDir  string
	StateFile   string
	WebURL      string
	Interval    time.Duration
	HTTPTimeout time.Duration
}

type Runner struct {
	config      Config
	client      *http.Client
	hashes      map[string]string
	stateLoaded bool
}

func New(config Config) *Runner {
	if config.StateFile == "" {
		config.StateFile = filepath.Join(config.ArchiveDir, ".discord-archive-importer-state.json")
	}
	return &Runner{config: config, client: &http.Client{Timeout: config.HTTPTimeout}, hashes: make(map[string]string)}
}

func (r *Runner) Run(ctx context.Context) error {
	delay := time.Duration(0)
	maxDelay := r.config.Interval * 16
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		if err := r.Sync(ctx); err != nil {
			log.Printf("sync failed; retrying: %v", err)
			if delay < r.config.Interval {
				delay = r.config.Interval
			} else {
				delay *= 2
			}
			if delay > maxDelay {
				delay = maxDelay
			}
			continue
		}
		delay = r.config.Interval
	}
}

var guildPattern = regexp.MustCompile(`^guild_id=(.+)$`)
var datePattern = regexp.MustCompile(`^date=(\d{4}-\d{2}-\d{2})$`)

func (r *Runner) Sync(ctx context.Context) error {
	if err := r.loadState(); err != nil {
		return err
	}
	entries, err := os.ReadDir(r.config.ArchiveDir)
	if err != nil {
		return fmt.Errorf("read archive: %w", err)
	}
	var failures []error
	for _, entry := range entries {
		match := guildPattern.FindStringSubmatch(entry.Name())
		if !entry.IsDir() || match == nil {
			continue
		}
		guildID := match[1]
		root := filepath.Join(r.config.ArchiveDir, entry.Name())
		if err := r.syncMetadata(ctx, guildID, root); err != nil {
			failures = append(failures, err)
		}
		dateEntries, err := os.ReadDir(filepath.Join(root, "messages"))
		if err != nil && !os.IsNotExist(err) {
			failures = append(failures, err)
			continue
		}
		for _, dateEntry := range dateEntries {
			dateMatch := datePattern.FindStringSubmatch(dateEntry.Name())
			if !dateEntry.IsDir() || dateMatch == nil {
				continue
			}
			if err := r.syncDate(ctx, guildID, dateMatch[1], filepath.Join(root, "messages", dateEntry.Name())); err != nil {
				failures = append(failures, err)
			}
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d snapshot(s) failed: %v", len(failures), failures[0])
	}
	return nil
}

func (r *Runner) syncMetadata(ctx context.Context, guildID, root string) error {
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	for _, spec := range []struct{ name, kind, field string }{{"channels.jsonl", "channel", "channel"}, {"threads.jsonl", "thread", "thread"}} {
		path := filepath.Join(root, "metadata", spec.name)
		if err := transformLines(path, func(raw json.RawMessage) error {
			return enc.Encode(map[string]any{"kind": spec.kind, spec.field: raw})
		}); err != nil {
			return err
		}
	}
	return r.sendChanged(ctx, "metadata:"+guildID, "/api/v1/import/guilds/"+url.PathEscape(guildID)+"/metadata", body.Bytes())
}

func (r *Runner) syncDate(ctx context.Context, guildID, date, root string) error {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == "messages.jsonl" {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(paths)
	var body bytes.Buffer
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(&body, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	key := "date:" + guildID + ":" + date
	path := "/api/v1/import/guilds/" + url.PathEscape(guildID) + "/dates/" + date
	return r.sendChanged(ctx, key, path, body.Bytes())
}

func transformLines(path string, fn func(json.RawMessage) error) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if !json.Valid(line) {
			return fmt.Errorf("invalid JSON in %s", path)
		}
		if err := fn(json.RawMessage(line)); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) sendChanged(ctx context.Context, key, path string, body []byte) error {
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	if r.hashes[key] == hash {
		return nil
	}
	base := strings.TrimRight(r.config.WebURL, "/")
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-ndjson")
	response, err := r.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("PUT %s: %s: %s", path, response.Status, strings.TrimSpace(string(message)))
	}
	next := make(map[string]string, len(r.hashes)+1)
	for existingKey, existingHash := range r.hashes {
		next[existingKey] = existingHash
	}
	next[key] = hash
	if err := r.saveState(next); err != nil {
		return fmt.Errorf("save importer state: %w", err)
	}
	r.hashes = next
	log.Printf("synchronized %s", key)
	return nil
}

type persistedState struct {
	Version int               `json:"version"`
	Hashes  map[string]string `json:"hashes"`
}

func (r *Runner) loadState() error {
	if r.stateLoaded {
		return nil
	}
	data, err := os.ReadFile(r.config.StateFile)
	if os.IsNotExist(err) {
		r.stateLoaded = true
		return nil
	}
	if err != nil {
		return fmt.Errorf("read importer state: %w", err)
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode importer state: %w", err)
	}
	if state.Version != 1 || state.Hashes == nil {
		return fmt.Errorf("decode importer state: unsupported state version %d", state.Version)
	}
	r.hashes = state.Hashes
	r.stateLoaded = true
	return nil
}

func (r *Runner) saveState(hashes map[string]string) error {
	state := persistedState{Version: 1, Hashes: hashes}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(r.config.StateFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, ".importer-state-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, r.config.StateFile)
}
