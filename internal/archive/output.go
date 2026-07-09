package archive

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type archiveOutput struct {
	guildRoot     string
	messagesRoot  string
	dateFilter    *dateFilter
	dateTempDir   string
	dateTargetDir string
	dateBackupDir string
	messageFiles  map[string]*jsonFile
	metadataFiles map[string]*jsonFile
}

type jsonFile struct {
	file    *os.File
	encoder *json.Encoder
}

func newArchiveOutput(outputDir, guildID string, filter *dateFilter) (*archiveOutput, error) {
	guildRoot := filepath.Join(outputDir, "guild_id="+guildID)
	messagesRoot := filepath.Join(guildRoot, "messages")

	output := &archiveOutput{
		guildRoot:     guildRoot,
		messagesRoot:  messagesRoot,
		dateFilter:    filter,
		messageFiles:  make(map[string]*jsonFile),
		metadataFiles: make(map[string]*jsonFile),
	}

	if err := os.MkdirAll(filepath.Join(guildRoot, "metadata"), 0o755); err != nil {
		return nil, fmt.Errorf("create metadata directory: %w", err)
	}
	if err := os.MkdirAll(messagesRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create messages directory: %w", err)
	}

	if filter != nil {
		output.dateTargetDir = filepath.Join(messagesRoot, "date="+filter.Date)
		output.dateTempDir = filepath.Join(messagesRoot, fmt.Sprintf(".date=%s.tmp-%d-%d", filter.Date, os.Getpid(), time.Now().UnixNano()))
		output.dateBackupDir = filepath.Join(messagesRoot, fmt.Sprintf(".date=%s.backup-%d-%d", filter.Date, os.Getpid(), time.Now().UnixNano()))
		if err := os.MkdirAll(output.dateTempDir, 0o755); err != nil {
			return nil, fmt.Errorf("create temporary date directory: %w", err)
		}
	}

	return output, nil
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

func (o *archiveOutput) metadataFile(name string) (*jsonFile, error) {
	path := filepath.Join(o.guildRoot, "metadata", name)
	if file, ok := o.metadataFiles[path]; ok {
		return file, nil
	}

	file, err := newJSONFile(path)
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
		if err := file.file.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	for _, file := range o.metadataFiles {
		if err := file.file.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	o.messageFiles = nil
	o.metadataFiles = nil
	return errors.Join(errs...)
}

func (o *archiveOutput) Commit() error {
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

func (o *archiveOutput) Cleanup() {
	_ = o.Close()
	if o.dateTempDir != "" {
		_ = os.RemoveAll(o.dateTempDir)
	}
}
