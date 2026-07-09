package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
)

const jstLocation = "Asia/Tokyo"
const defaultScheduleTime = "03:00"

type commandMode string

const (
	commandDaemon commandMode = "daemon"
	commandDump   commandMode = "dump"
)

type archiveConfig struct {
	token          string
	guildID        string
	outputDir      string
	includeThreads bool
	includePrivate bool
	excludePrivate bool
}

type commandConfig struct {
	mode         commandMode
	archive      archiveConfig
	date         string
	scheduleTime string
	timezone     string
	runOnStart   bool
	noRunOnStart bool
}

type archiveRecord struct {
	GuildID     string                `json:"guild_id"`
	ChannelID   string                `json:"channel_id"`
	ChannelName string                `json:"channel_name,omitempty"`
	ChannelType discordgo.ChannelType `json:"channel_type"`
	ParentID    string                `json:"parent_id,omitempty"`
	Message     *discordgo.Message    `json:"message"`
}

type channelRecord struct {
	GuildID string             `json:"guild_id"`
	Channel *discordgo.Channel `json:"channel"`
}

type threadRecord struct {
	GuildID string             `json:"guild_id"`
	Source  string             `json:"source"`
	Thread  *discordgo.Channel `json:"thread"`
}

type dateFilter struct {
	Date  string
	Start time.Time
	End   time.Time
}

type archiver struct {
	session            *discordgo.Session
	guildID            string
	includeThreads     bool
	includePrivate     bool
	partitionLocation  *time.Location
	dateFilter         *dateFilter
	output             *archiveOutput
	seenChannels       map[string]struct{}
	seenThreadMetadata map[string]struct{}
}

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

func main() {
	if err := loadDotEnv(".env"); err != nil {
		log.Fatal(err)
	}

	config, err := parseCommand(os.Args[1:], os.Getenv)
	if err != nil {
		log.Fatal(err)
	}

	if err := executeCommand(config); err != nil {
		log.Fatal(err)
	}
}

func parseCommand(args []string, getenv func(string) string) (commandConfig, error) {
	mode := commandDaemon
	if len(args) > 0 {
		switch args[0] {
		case "daemon":
			args = args[1:]
		case "dump":
			mode = commandDump
			args = args[1:]
		}
	}

	config := commandConfig{
		mode:         mode,
		scheduleTime: valueOrDefault(getenv("DISCORD_ARCHIVER_SCHEDULE_TIME"), defaultScheduleTime),
		timezone:     valueOrDefault(getenv("TZ"), jstLocation),
		runOnStart:   envBoolDefault(getenv("DISCORD_ARCHIVER_RUN_ON_START"), true),
	}

	flags := flag.NewFlagSet("discord-archiver", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	addArchiveFlags(flags, &config.archive)

	switch mode {
	case commandDaemon:
		flags.StringVar(&config.scheduleTime, "schedule-time", config.scheduleTime, "Daily archive time in HH:MM.")
		flags.StringVar(&config.timezone, "timezone", config.timezone, "Schedule timezone IANA name.")
		flags.BoolVar(&config.runOnStart, "run-on-start", config.runOnStart, "Run yesterday archive immediately on daemon start.")
		flags.BoolVar(&config.noRunOnStart, "no-run-on-start", false, "Skip the immediate archive on daemon start.")
	case commandDump:
		var all bool
		flags.BoolVar(&all, "all", false, "Archive all visible history.")
		flags.StringVar(&config.date, "date", "", "JST date to refresh in YYYY-MM-DD format.")
		if err := flags.Parse(args); err != nil {
			return commandConfig{}, err
		}
		if all == (strings.TrimSpace(config.date) != "") {
			return commandConfig{}, errors.New("dump requires exactly one of -all or -date")
		}
		if flags.NArg() > 0 {
			return commandConfig{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
		}
		resolveArchiveConfig(&config.archive, getenv)
		return config, nil
	default:
		return commandConfig{}, fmt.Errorf("unknown command mode %q", mode)
	}

	if err := flags.Parse(args); err != nil {
		return commandConfig{}, err
	}
	if flags.NArg() > 0 {
		return commandConfig{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	if config.noRunOnStart {
		config.runOnStart = false
	}
	resolveArchiveConfig(&config.archive, getenv)
	if _, err := parseScheduleClock(config.scheduleTime); err != nil {
		return commandConfig{}, err
	}
	if _, err := time.LoadLocation(config.timezone); err != nil {
		return commandConfig{}, fmt.Errorf("load schedule timezone %q: %w", config.timezone, err)
	}
	return config, nil
}

func addArchiveFlags(flags *flag.FlagSet, config *archiveConfig) {
	flags.StringVar(&config.token, "token", "", "Discord bot token. Defaults to DISCORD_BOT_TOKEN.")
	flags.StringVar(&config.guildID, "guild", "", "Discord guild/server ID. Defaults to DISCORD_GUILD_ID.")
	flags.StringVar(&config.outputDir, "out-dir", "archive", "Output archive directory path.")
	flags.BoolVar(&config.includeThreads, "threads", true, "Include active and archived threads.")
	flags.BoolVar(&config.excludePrivate, "no-private-threads", false, "Exclude private archived threads visible to the bot.")
}

func resolveArchiveConfig(config *archiveConfig, getenv func(string) string) {
	config.token = valueOrDefault(config.token, getenv("DISCORD_BOT_TOKEN"))
	config.guildID = valueOrDefault(config.guildID, getenv("DISCORD_GUILD_ID"))
	config.includePrivate = !config.excludePrivate
}

func executeCommand(config commandConfig) error {
	switch config.mode {
	case commandDaemon:
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return runDaemon(ctx, config)
	case commandDump:
		return runArchive(config.archive, config.date)
	default:
		return fmt.Errorf("unknown command mode %q", config.mode)
	}
}

func runArchive(config archiveConfig, date string) error {
	return run(config.token, config.guildID, config.outputDir, date, config.includeThreads, config.includePrivate)
}

func validateArchiveConfig(config archiveConfig) error {
	if config.token == "" {
		return errors.New("missing Discord bot token: set DISCORD_BOT_TOKEN or pass -token")
	}
	if config.guildID == "" {
		return errors.New("missing Discord guild ID: set DISCORD_GUILD_ID or pass -guild")
	}
	return nil
}

func runDaemon(ctx context.Context, config commandConfig) error {
	if err := validateArchiveConfig(config.archive); err != nil {
		return err
	}

	loc, err := time.LoadLocation(config.timezone)
	if err != nil {
		return fmt.Errorf("load schedule timezone %q: %w", config.timezone, err)
	}
	schedule, err := parseScheduleClock(config.scheduleTime)
	if err != nil {
		return err
	}

	if config.runOnStart {
		runScheduledArchive(config.archive, previousDate(time.Now().In(loc), loc))
	}

	for {
		next := nextScheduledTime(time.Now().In(loc), schedule, loc)
		log.Printf("next scheduled archive at %s", next.Format(time.RFC3339))
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
			runScheduledArchive(config.archive, previousDate(time.Now().In(loc), loc))
		}
	}
}

func runScheduledArchive(config archiveConfig, date string) {
	log.Printf("starting scheduled archive for %s", date)
	if err := runArchive(config, date); err != nil {
		log.Printf("scheduled archive for %s failed: %v", date, err)
		return
	}
	log.Printf("finished scheduled archive for %s", date)
}

type scheduleClock struct {
	hour   int
	minute int
}

func parseScheduleClock(value string) (scheduleClock, error) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 2 {
		return scheduleClock{}, fmt.Errorf("parse schedule time %q: expected HH:MM", value)
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return scheduleClock{}, fmt.Errorf("parse schedule hour %q: %w", parts[0], err)
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return scheduleClock{}, fmt.Errorf("parse schedule minute %q: %w", parts[1], err)
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return scheduleClock{}, fmt.Errorf("parse schedule time %q: expected HH:MM in 24-hour time", value)
	}
	return scheduleClock{hour: hour, minute: minute}, nil
}

func nextScheduledTime(now time.Time, schedule scheduleClock, loc *time.Location) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), schedule.hour, schedule.minute, 0, 0, loc)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func previousDate(now time.Time, loc *time.Location) string {
	return now.In(loc).AddDate(0, 0, -1).Format("2006-01-02")
}

func valueOrEnv(value, envName string) string {
	if value != "" {
		return value
	}
	return os.Getenv(envName)
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func envBoolDefault(value string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return fallback
	case "1", "true", "t", "yes", "y", "on":
		return true
	case "0", "false", "f", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("invalid .env line: %q", line)
		}

		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf("invalid .env line with empty key: %q", line)
		}

		value = strings.TrimSpace(value)
		value = strings.Trim(value, "\"'")

		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set %s from .env: %w", key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	return nil
}

func run(token, guildID, outputDir, date string, includeThreads, includePrivate bool) error {
	if token == "" {
		return errors.New("missing Discord bot token: set DISCORD_BOT_TOKEN or pass -token")
	}
	if guildID == "" {
		return errors.New("missing Discord guild ID: set DISCORD_GUILD_ID or pass -guild")
	}

	partitionLocation, err := time.LoadLocation(jstLocation)
	if err != nil {
		return fmt.Errorf("load %s location: %w", jstLocation, err)
	}

	filter, err := parseDateFilter(date, partitionLocation)
	if err != nil {
		return err
	}

	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return fmt.Errorf("create discord session: %w", err)
	}

	output, err := newArchiveOutput(outputDir, guildID, filter)
	if err != nil {
		return err
	}
	defer output.Cleanup()

	a := &archiver{
		session:            session,
		guildID:            guildID,
		includeThreads:     includeThreads,
		includePrivate:     includePrivate,
		partitionLocation:  partitionLocation,
		dateFilter:         filter,
		output:             output,
		seenChannels:       make(map[string]struct{}),
		seenThreadMetadata: make(map[string]struct{}),
	}

	channels, err := session.GuildChannels(guildID)
	if err != nil {
		return fmt.Errorf("list guild channels: %w", err)
	}
	for _, channel := range channels {
		if err := output.WriteChannelMetadata(channelRecord{GuildID: guildID, Channel: channel}); err != nil {
			return err
		}
	}

	for _, channel := range channels {
		if !canContainMessages(channel.Type) {
			continue
		}
		if err := a.archiveChannel(channel); err != nil {
			log.Printf("archive channel %s (%s): %v", channel.Name, channel.ID, err)
		}
	}

	if includeThreads {
		if err := a.archiveThreads(channels); err != nil {
			return err
		}
	}

	if err := output.Close(); err != nil {
		return err
	}
	if err := output.Commit(); err != nil {
		return err
	}

	return nil
}

func parseDateFilter(date string, loc *time.Location) (*dateFilter, error) {
	date = strings.TrimSpace(date)
	if date == "" {
		return nil, nil
	}

	start, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return nil, fmt.Errorf("parse -date %q: expected YYYY-MM-DD: %w", date, err)
	}
	return &dateFilter{
		Date:  date,
		Start: start,
		End:   start.AddDate(0, 0, 1),
	}, nil
}

func canContainMessages(channelType discordgo.ChannelType) bool {
	switch channelType {
	case discordgo.ChannelTypeGuildText,
		discordgo.ChannelTypeGuildNews,
		discordgo.ChannelTypeGuildVoice,
		discordgo.ChannelTypeGuildNewsThread,
		discordgo.ChannelTypeGuildPublicThread,
		discordgo.ChannelTypeGuildPrivateThread:
		return true
	default:
		return false
	}
}

func canContainThreads(channelType discordgo.ChannelType) bool {
	switch channelType {
	case discordgo.ChannelTypeGuildText,
		discordgo.ChannelTypeGuildNews,
		discordgo.ChannelTypeGuildForum,
		discordgo.ChannelTypeGuildMedia:
		return true
	default:
		return false
	}
}

// channelMessagesCompat mirrors discordgo.Session.ChannelMessages while working around
// discordgo v0.29.0 failing on newly introduced Discord component types.
//
// Remove this wrapper and call session.ChannelMessages directly once discordgo can
// unmarshal unknown/future message component types without failing the whole page.
func channelMessagesCompat(session *discordgo.Session, channelID string, limit int, beforeID, afterID, aroundID string) ([]*discordgo.Message, error) {
	uri := discordgo.EndpointChannelMessages(channelID)

	values := url.Values{}
	if limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}
	if afterID != "" {
		values.Set("after", afterID)
	}
	if beforeID != "" {
		values.Set("before", beforeID)
	}
	if aroundID != "" {
		values.Set("around", aroundID)
	}
	if len(values) > 0 {
		uri += "?" + values.Encode()
	}

	body, err := session.RequestWithBucketID("GET", uri, nil, discordgo.EndpointChannelMessages(channelID))
	if err != nil {
		return nil, err
	}

	return unmarshalMessagesWithoutComponents(body)
}

// unmarshalMessagesWithoutComponents drops components before decoding because
// discordgo.Message does not marshal Components back to JSON anyway.
func unmarshalMessagesWithoutComponents(body []byte) ([]*discordgo.Message, error) {
	var rawMessages []map[string]json.RawMessage
	if err := json.Unmarshal(body, &rawMessages); err != nil {
		return nil, err
	}

	messages := make([]*discordgo.Message, 0, len(rawMessages))
	for _, rawMessage := range rawMessages {
		delete(rawMessage, "components")

		messageBody, err := json.Marshal(rawMessage)
		if err != nil {
			return nil, err
		}

		var message discordgo.Message
		if err := json.Unmarshal(messageBody, &message); err != nil {
			return nil, err
		}
		messages = append(messages, &message)
	}

	return messages, nil
}

func (a *archiver) archiveChannel(channel *discordgo.Channel) error {
	if channel == nil {
		return nil
	}
	if _, ok := a.seenChannels[channel.ID]; ok {
		return nil
	}
	a.seenChannels[channel.ID] = struct{}{}

	log.Printf("archiving channel %s (%s)", channel.Name, channel.ID)

	var beforeID string
	for {
		messages, err := channelMessagesCompat(a.session, channel.ID, 100, beforeID, "", "")
		if err != nil {
			return err
		}
		if len(messages) == 0 {
			return nil
		}

		stopChannel := false
		for _, message := range messages {
			messageTime, err := messageTimestamp(message)
			if err != nil {
				return err
			}

			include, olderThanFilter := a.shouldArchiveMessage(messageTime)
			if olderThanFilter {
				stopChannel = true
				break
			}
			if !include {
				continue
			}

			record := archiveRecord{
				GuildID:     a.guildID,
				ChannelID:   channel.ID,
				ChannelName: channel.Name,
				ChannelType: channel.Type,
				ParentID:    channel.ParentID,
				Message:     message,
			}
			date := partitionDate(messageTime, a.partitionLocation)
			if err := a.output.WriteMessage(date, channel.ID, record); err != nil {
				return err
			}
		}

		if stopChannel {
			return nil
		}
		beforeID = messages[len(messages)-1].ID
	}
}

func (a *archiver) shouldArchiveMessage(messageTime time.Time) (include bool, olderThanFilter bool) {
	if a.dateFilter == nil {
		return true, false
	}
	if messageTime.Before(a.dateFilter.Start) {
		return false, true
	}
	if !messageTime.Before(a.dateFilter.End) {
		return false, false
	}
	return true, false
}

func messageTimestamp(message *discordgo.Message) (time.Time, error) {
	if message == nil {
		return time.Time{}, errors.New("nil message")
	}
	if !message.Timestamp.IsZero() {
		return message.Timestamp, nil
	}

	timestamp, err := discordgo.SnowflakeTimestamp(message.ID)
	if err != nil {
		return time.Time{}, fmt.Errorf("derive message timestamp from snowflake %q: %w", message.ID, err)
	}
	return timestamp, nil
}

func partitionDate(messageTime time.Time, loc *time.Location) string {
	return messageTime.In(loc).Format("2006-01-02")
}

func (a *archiver) archiveThreads(parentChannels []*discordgo.Channel) error {
	activeThreads, err := a.session.GuildThreadsActive(a.guildID)
	if err != nil {
		return fmt.Errorf("list active threads: %w", err)
	}
	for _, thread := range activeThreads.Threads {
		if err := a.writeThreadMetadata("active", thread); err != nil {
			return err
		}
		if err := a.archiveChannel(thread); err != nil {
			log.Printf("archive active thread %s (%s): %v", thread.Name, thread.ID, err)
		}
	}

	for _, parent := range parentChannels {
		if !canContainThreads(parent.Type) {
			continue
		}
		if err := a.archivePublicArchivedThreads(parent.ID); err != nil {
			log.Printf("archive public archived threads for %s (%s): %v", parent.Name, parent.ID, err)
		}
		if a.includePrivate {
			if err := a.archivePrivateArchivedThreads(parent.ID); err != nil {
				log.Printf("archive private archived threads for %s (%s): %v", parent.Name, parent.ID, err)
			}
		}
	}

	return nil
}

func (a *archiver) writeThreadMetadata(source string, thread *discordgo.Channel) error {
	if thread == nil {
		return nil
	}
	if _, ok := a.seenThreadMetadata[thread.ID]; ok {
		return nil
	}
	a.seenThreadMetadata[thread.ID] = struct{}{}
	return a.output.WriteThreadMetadata(threadRecord{GuildID: a.guildID, Source: source, Thread: thread})
}

func (a *archiver) archivePublicArchivedThreads(parentChannelID string) error {
	var before *time.Time
	for {
		threads, err := a.session.ThreadsArchived(parentChannelID, before, 100)
		if err != nil {
			return err
		}
		if len(threads.Threads) == 0 {
			return nil
		}

		for _, thread := range threads.Threads {
			if err := a.writeThreadMetadata("public_archived", thread); err != nil {
				return err
			}
			if err := a.archiveChannel(thread); err != nil {
				log.Printf("archive public archived thread %s (%s): %v", thread.Name, thread.ID, err)
			}
		}
		if !threads.HasMore {
			return nil
		}

		before = oldestArchiveTimestamp(threads.Threads)
		if before == nil {
			return nil
		}
	}
}

func (a *archiver) archivePrivateArchivedThreads(parentChannelID string) error {
	var before *time.Time
	for {
		threads, err := a.session.ThreadsPrivateArchived(parentChannelID, before, 100)
		if err != nil {
			return err
		}
		if len(threads.Threads) == 0 {
			return nil
		}

		for _, thread := range threads.Threads {
			if err := a.writeThreadMetadata("private_archived", thread); err != nil {
				return err
			}
			if err := a.archiveChannel(thread); err != nil {
				log.Printf("archive private archived thread %s (%s): %v", thread.Name, thread.ID, err)
			}
		}
		if !threads.HasMore {
			return nil
		}

		before = oldestArchiveTimestamp(threads.Threads)
		if before == nil {
			return nil
		}
	}
}

func oldestArchiveTimestamp(threads []*discordgo.Channel) *time.Time {
	var oldest *time.Time
	for _, thread := range threads {
		if thread == nil || thread.ThreadMetadata == nil || thread.ThreadMetadata.ArchiveTimestamp.IsZero() {
			continue
		}

		timestamp := thread.ThreadMetadata.ArchiveTimestamp
		if oldest == nil || timestamp.Before(*oldest) {
			oldest = &timestamp
		}
	}
	return oldest
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
