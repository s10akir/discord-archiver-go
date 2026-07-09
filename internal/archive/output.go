package archive

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

// staleTempMaxAge is how old a leftover temp/backup entry from a crashed run
// must be before a later run sweeps it. Generous so that a still-running
// concurrent archive is never deleted.
const staleTempMaxAge = 24 * time.Hour

// Temp/backup names end in "-<pid>-<unixnano>"; the captured group is the
// creation time used for the stale check.
var (
	staleDateDirPattern      = regexp.MustCompile(`^\.date=\d{4}-\d{2}-\d{2}\.(?:tmp|backup)-\d+-(\d+)$`)
	staleMetadataFilePattern = regexp.MustCompile(`^\..+\.tmp-\d+-(\d+)$`)
)

type archiveOutput struct {
	guildRoot     string
	metadataRoot  string
	messagesRoot  string
	dateFilter    *dateFilter
	tempSuffix    string
	dateTempDir   string
	dateTargetDir string
	dateBackupDir string
	messageFiles  map[string]*jsonFile
	metadataFiles map[string]*jsonFile
}

type jsonFile struct {
	file      *os.File
	encoder   *json.Encoder
	closed    bool
	committed bool // temp file has been renamed to its final path
}

func (f *jsonFile) Close() error {
	if f.closed {
		return nil
	}
	f.closed = true
	return f.file.Close()
}

func newArchiveOutput(outputDir, guildID string, filter *dateFilter) (*archiveOutput, error) {
	guildRoot := filepath.Join(outputDir, "guild_id="+guildID)

	output := &archiveOutput{
		guildRoot:     guildRoot,
		metadataRoot:  filepath.Join(guildRoot, "metadata"),
		messagesRoot:  filepath.Join(guildRoot, "messages"),
		dateFilter:    filter,
		tempSuffix:    fmt.Sprintf("tmp-%d-%d", os.Getpid(), time.Now().UnixNano()),
		messageFiles:  make(map[string]*jsonFile),
		metadataFiles: make(map[string]*jsonFile),
	}

	if err := os.MkdirAll(output.metadataRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create metadata directory: %w", err)
	}
	if err := os.MkdirAll(output.messagesRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create messages directory: %w", err)
	}

	removeStaleTempEntries(output.messagesRoot, staleDateDirPattern)
	removeStaleTempEntries(output.metadataRoot, staleMetadataFilePattern)

	if filter != nil {
		output.dateTargetDir = filepath.Join(output.messagesRoot, "date="+filter.Date)
		output.dateTempDir = filepath.Join(output.messagesRoot, fmt.Sprintf(".date=%s.%s", filter.Date, output.tempSuffix))
		output.dateBackupDir = filepath.Join(output.messagesRoot, fmt.Sprintf(".date=%s.backup-%d-%d", filter.Date, os.Getpid(), time.Now().UnixNano()))
		if err := os.MkdirAll(output.dateTempDir, 0o755); err != nil {
			return nil, fmt.Errorf("create temporary date directory: %w", err)
		}
	}

	return output, nil
}

// removeStaleTempEntries deletes temp/backup leftovers from crashed runs.
// Entries younger than staleTempMaxAge — including everything the current
// run just created — are kept, so an archive still in progress is safe.
func removeStaleTempEntries(dir string, pattern *regexp.Regexp) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		match := pattern.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		nanos, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			continue
		}
		if time.Since(time.Unix(0, nanos)) < staleTempMaxAge {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			log.Printf("remove stale temp entry %s: %v", path, err)
			continue
		}
		log.Printf("removed stale temp entry %s", path)
	}
}

func (o *archiveOutput) WriteChannelMetadata(record channelRecord) error {
	file, err := o.metadataFile("channels.jsonl")
	if err != nil {
		return err
	}
	return file.encoder.Encode(record)
}

func (o *archiveOutput) WriteThreadMetadata(record threadRecord) error {
	file, err := o.metadataFile("threads.jsonl")
	if err != nil {
		return err
	}
	return file.encoder.Encode(record)
}

func (o *archiveOutput) WriteMessage(date, channelID string, record archiveRecord) error {
	file, err := o.messageFile(date, channelID)
	if err != nil {
		return err
	}
	return file.encoder.Encode(record)
}

// metadataFile stages writes in a hidden temp file next to the final path;
// Commit renames it over the previous version so a failed run never leaves a
// half-written channels.jsonl/threads.jsonl behind.
func (o *archiveOutput) metadataFile(name string) (*jsonFile, error) {
	path := filepath.Join(o.metadataRoot, name)
	if file, ok := o.metadataFiles[path]; ok {
		return file, nil
	}

	file, err := newJSONFile(filepath.Join(o.metadataRoot, fmt.Sprintf(".%s.%s", name, o.tempSuffix)))
	if err != nil {
		return nil, err
	}
	o.metadataFiles[path] = file
	return file, nil
}

func (o *archiveOutput) messageFile(date, channelID string) (*jsonFile, error) {
	var dateDir string
	if o.dateFilter != nil && date == o.dateFilter.Date {
		dateDir = o.dateTempDir
	} else {
		dateDir = filepath.Join(o.messagesRoot, "date="+date)
	}

	path := filepath.Join(dateDir, "channel_id="+channelID, "messages.jsonl")
	if file, ok := o.messageFiles[path]; ok {
		return file, nil
	}

	file, err := newJSONFile(path)
	if err != nil {
		return nil, err
	}
	o.messageFiles[path] = file
	return file, nil
}

func newJSONFile(path string) (*jsonFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create directory for %s: %w", path, err)
	}

	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", path, err)
	}
	return &jsonFile{file: file, encoder: json.NewEncoder(file)}, nil
}

func (o *archiveOutput) Close() error {
	var errs []error
	for _, file := range o.messageFiles {
		if err := file.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	for _, file := range o.metadataFiles {
		if err := file.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (o *archiveOutput) Commit() error {
	if err := o.commitDatePartition(); err != nil {
		return err
	}
	return o.commitMetadata()
}

func (o *archiveOutput) commitDatePartition() error {
	if o.dateFilter == nil {
		return nil
	}

	if _, err := os.Stat(o.dateTargetDir); err == nil {
		if err := os.Rename(o.dateTargetDir, o.dateBackupDir); err != nil {
			return fmt.Errorf("move existing date directory aside: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat existing date directory: %w", err)
	}

	if err := os.Rename(o.dateTempDir, o.dateTargetDir); err != nil {
		if o.dateBackupDir != "" {
			_ = os.Rename(o.dateBackupDir, o.dateTargetDir)
		}
		return fmt.Errorf("replace date directory: %w", err)
	}
	o.dateTempDir = ""

	if o.dateBackupDir != "" {
		if err := os.RemoveAll(o.dateBackupDir); err != nil {
			return fmt.Errorf("remove date directory backup: %w", err)
		}
	}

	return nil
}

func (o *archiveOutput) commitMetadata() error {
	for path, file := range o.metadataFiles {
		if file.committed {
			continue
		}
		if err := os.Rename(file.file.Name(), path); err != nil {
			return fmt.Errorf("replace %s: %w", path, err)
		}
		file.committed = true
	}
	return nil
}

func (o *archiveOutput) Cleanup() {
	_ = o.Close()
	if o.dateTempDir != "" {
		_ = os.RemoveAll(o.dateTempDir)
	}
	for _, file := range o.metadataFiles {
		if !file.committed {
			_ = os.Remove(file.file.Name())
		}
	}
}
