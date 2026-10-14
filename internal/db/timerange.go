package db

import "time"

// TimeRange is the span of time a range method covers; a range method is a
// Provider method taking a TimeRange. Build one with NewTimeRange: the zero
// value means no range was given, and every range method rejects it with
// ErrTimeRangeRequired.
type TimeRange struct {
	from, to time.Time
}

// NewTimeRange returns the range from from to to, or ErrTimeRangeRequired
// when either bound is missing.
func NewTimeRange(from, to time.Time) (TimeRange, error) {
	if from.IsZero() || to.IsZero() {
		return TimeRange{}, ErrTimeRangeRequired
	}
	return TimeRange{from: from, to: to}, nil
}

// TimeRangeUntilNow returns the range covering span up to now.
func TimeRangeUntilNow(span time.Duration) TimeRange {
	now := time.Now().UTC()
	return TimeRange{from: now.Add(-span), to: now}
}

// From is the range's start.
func (tr TimeRange) From() time.Time { return tr.from }

// To is the range's end.
func (tr TimeRange) To() time.Time { return tr.to }

// IsZero reports whether tr is the zero value: no range was given.
func (tr TimeRange) IsZero() bool { return tr.from.IsZero() }

// Format returns From and To in UTC, formatted with layout.
func (tr TimeRange) Format(layout string) (string, string) {
	return tr.from.UTC().Format(layout), tr.to.UTC().Format(layout)
}

// Previous returns the range of the same length ending at tr's start.
func (tr TimeRange) Previous() TimeRange {
	duration := tr.to.Sub(tr.from)
	return TimeRange{from: tr.from.Add(-duration), to: tr.to.Add(-duration)}
}
