package archive

import (
	"fmt"
	"strings"
	"time"
)

type dateFilter struct {
	Date  string
	Start time.Time
	End   time.Time
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

func partitionDate(messageTime time.Time, loc *time.Location) string {
	return messageTime.In(loc).Format("2006-01-02")
}
