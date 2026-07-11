package archive

import (
	"fmt"
	"sync"

	"github.com/bwmarrin/discordgo"
)

// attachmentConcurrency bounds how many attachment downloads run at once so
// channels with heavy media use don't serialize archiving behind network I/O,
// while still limiting how many concurrent requests hit Discord's CDN.
const attachmentConcurrency = 4

// attachmentPool runs attachment downloads concurrently. Errors are collected
// rather than propagated immediately so one failed download does not stop
// the others or abort the archive pass.
type attachmentPool struct {
	sem  chan struct{}
	wg   sync.WaitGroup
	mu   sync.Mutex
	errs []error
}

func newAttachmentPool() *attachmentPool {
	return &attachmentPool{sem: make(chan struct{}, attachmentConcurrency)}
}

func (p *attachmentPool) submit(fn func() error) {
	p.sem <- struct{}{}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() { <-p.sem }()
		if err := fn(); err != nil {
			p.mu.Lock()
			p.errs = append(p.errs, err)
			p.mu.Unlock()
		}
	}()
}

// wait blocks until every submitted download has finished and returns their
// errors. Callers must not submit further jobs after calling wait.
func (p *attachmentPool) wait() []error {
	p.wg.Wait()
	return p.errs
}

// queueAttachments schedules a background download for each of message's
// attachments, reusing an already-archived copy when one exists at the same
// size instead of re-fetching it.
func (a *archiver) queueAttachments(date, channelID string, message *discordgo.Message) {
	if !a.downloadAttachments || len(message.Attachments) == 0 {
		return
	}

	messageID := message.ID
	for _, attachment := range message.Attachments {
		a.attachments.submit(func() error {
			writePath, upToDate := a.output.AttachmentUpToDate(date, channelID, messageID, attachment)
			if upToDate {
				return nil
			}

			body, err := a.client.DownloadAttachment(attachment.URL)
			if err != nil {
				return fmt.Errorf("download attachment %s (%s) for message %s in channel %s: %w", attachment.Filename, attachment.ID, messageID, channelID, err)
			}
			defer body.Close()

			if err := a.output.WriteAttachment(writePath, body); err != nil {
				return fmt.Errorf("write attachment %s (%s) for message %s in channel %s: %w", attachment.Filename, attachment.ID, messageID, channelID, err)
			}
			return nil
		})
	}
}
