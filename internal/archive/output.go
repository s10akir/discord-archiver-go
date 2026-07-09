package archive

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"
)

// archiveWriter is the output-side boundary: where archived records go.
// archiveOutput implements it with local JSONL files; alternative
// destinations (e.g. S3-compatible storage) can implement it without
// touching the fetch logic.
type archiveWriter interface {
	WriteChannelMetadata(record channelRecord) error
	WriteThreadMetadata(record threadRecord) error
	WriteMessage(date, channelID string, record archiveRecord) error
	// AttachmentUpToDate reports whether a same-size copy of attachment
	// already exists in the committed archive, reusing it in place (copying
	// it forward into the staged date partition when one is in play) so a
	// rerun does not re-download unchanged attachments. It always returns
	// the path attachment content must be written to when not up to date.
	AttachmentUpToDate(date, channelID, messageID string, attachment *discordgo.MessageAttachment) (writePath string, upToDate bool)
	// WriteAttachment writes body to writePath (as returned by
	// AttachmentUpToDate), creating parent directories as needed.
	WriteAttachment(writePath string, body io.Reader) error
	// Close flushes and closes open files. Commit publishes staged output
	// (temp date partition, temp metadata files) and must only be called
	// after Close succeeds. Cleanup discards anything left unpublished.
	Close() error
	Commit() error
	Cleanup()
}

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

// attachmentFilenamePattern matches characters not safe to embed verbatim in
// a path component; anything else is replaced with "_".
var attachmentFilenamePattern = regexp.MustCompile(`[/\\\x00]`)

func sanitizeAttachmentFilename(filename string) string {
	base := filepath.Base(filename)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "file"
	}
	return attachmentFilenamePattern.ReplaceAllString(base, "_")
}

// AttachmentRelPath returns the path, relative to a guild's root directory,
// at which an attachment for (date, channelID, messageID) is stored once
// committed. Consumers that only read the archive (e.g. a viewer) can use it
// to locate attachment files without duplicating the on-disk layout.
func AttachmentRelPath(date, channelID, messageID string, attachment *discordgo.MessageAttachment) string {
	name := attachment.ID + "-" + sanitizeAttachmentFilename(attachment.Filename)
	return filepath.Join("messages", "date="+date, "channel_id="+channelID, "attachments", messageID, name)
}

// attachmentPaths returns the directory an attachment for (date, channelID,
// messageID) lives in, both in the tree being written this run (dir) and in
// the currently committed archive (committedDir). The two differ only while
// date is being staged in a temp directory ahead of an atomic replace.
func (o *archiveOutput) attachmentPaths(date, channelID, messageID string, attachment *discordgo.MessageAttachment) (writePath, committedPath string) {
	name := attachment.ID + "-" + sanitizeAttachmentFilename(attachment.Filename)

	if o.dateFilter != nil && date == o.dateFilter.Date {
		writePath = filepath.Join(o.dateTempDir, "channel_id="+channelID, "attachments", messageID, name)
		committedPath = filepath.Join(o.dateTargetDir, "channel_id="+channelID, "attachments", messageID, name)
		return writePath, committedPath
	}

	writePath = filepath.Join(o.messagesRoot, "date="+date, "channel_id="+channelID, "attachments", messageID, name)
	return writePath, writePath
}

func (o *archiveOutput) AttachmentUpToDate(date, channelID, messageID string, attachment *discordgo.MessageAttachment) (writePath string, upToDate bool) {
	writePath, committedPath := o.attachmentPaths(date, channelID, messageID, attachment)

	info, err := os.Stat(committedPath)
	if err != nil || info.Size() != int64(attachment.Size) {
		return writePath, false
	}
	if committedPath == writePath {
		return writePath, true
	}

	existing, err := os.Open(committedPath)
	if err != nil {
		return writePath, false
	}
	defer existing.Close()
	if err := o.WriteAttachment(writePath, existing); err != nil {
		return writePath, false
	}
	return writePath, true
}

func (o *archiveOutput) WriteAttachment(writePath string, body io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(writePath), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", writePath, err)
	}

	file, err := os.Create(writePath)
	if err != nil {
		return fmt.Errorf("create %s: %w", writePath, err)
	}
	defer file.Close()

	if _, err := io.Copy(file, body); err != nil {
		return fmt.Errorf("write %s: %w", writePath, err)
	}
	return nil
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
