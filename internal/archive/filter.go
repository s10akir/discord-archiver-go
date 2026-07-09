package archive

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type dateFilter struct {
	Date  string
	Start time.Time
	End   time.Time
	// SeekBeforeID is a synthetic Discord snowflake marking the end of the
	// filtered date. Passing it as the initial "before" cursor lets
	// archiveChannel jump straight to the target date instead of paging
	// from the channel's newest message.
	SeekBeforeID string
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
	end := start.AddDate(0, 0, 1)
	return &dateFilter{
		Date:         date,
		Start:        start,
		End:          end,
		SeekBeforeID: snowflakeBefore(end),
	}, nil
}

// discordEpochMillis is the Unix time (ms) that Discord snowflake IDs are
// relative to, per Discord's documented snowflake format.
const discordEpochMillis int64 = 1420070400000

// snowflakeBefore returns the smallest Discord snowflake ID that could have
// been generated at t (the timestamp bits with worker/process/increment all
// zero). Discord's message-list endpoint accepts any snowflake-shaped ID as
// a before/after/around cursor — this is the technique Discord's own API
// docs describe for seeking to a point in time without an existing message
// ID there.
func snowflakeBefore(t time.Time) string {
	ms := t.UnixMilli() - discordEpochMillis
	if ms < 0 {
		ms = 0
	}
	return strconv.FormatInt(ms<<22, 10)
}

func partitionDate(messageTime time.Time, loc *time.Location) string {
	return messageTime.In(loc).Format("2006-01-02")
}
