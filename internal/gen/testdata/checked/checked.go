// Package checked maps to and from validated values.
package checked

import (
	"net/url"
	"time"

	"github.com/go-kanna/validated"
	"github.com/go-kanna/validated/rules"

	"github.com/go-kanna/mapper/internal/gen/testdata/checked/private"
)

// CompletedAt holds a time CompletedAtRule accepts.
type CompletedAt = validated.Value[time.Time, CompletedAtRule]

// CompletedAtRule accepts a time from 2000 on.
type CompletedAtRule struct{}

func (CompletedAtRule) Validate(t time.Time) error {
	if t.Year() < 2000 {
		return validated.Fail("completed_at")
	}
	return nil
}

// Count's rule is unexported, which this package can still name.
type Count = validated.Int64[countRule]

type countRule struct{}

func (countRule) Validate(n int64) error {
	if n < 0 {
		return validated.Fail("count")
	}
	return nil
}

type (
	Email   = validated.String[rules.Email]
	Website = validated.String[rules.URL]
)

// Timestamp stands in for timestamppb.Timestamp.
type Timestamp struct{ Seconds int64 }

// NewTimestamp is timestamppb.New.
func NewTimestamp(t time.Time) *Timestamp { return &Timestamp{Seconds: t.Unix()} }

// AsTime is (*timestamppb.Timestamp).AsTime.
func (x *Timestamp) AsTime() time.Time {
	if x == nil {
		return time.Unix(0, 0).UTC()
	}
	return time.Unix(x.Seconds, 0).UTC()
}

// parseEmail is listed for the pair it converts, which it takes over from
// validated.New.
func parseEmail(s string) (Email, error) { return validated.New[rules.Email](s) }

//kanna:converter
var _ = []any{
	NewTimestamp,
	(*Timestamp).AsTime,
	parseEmail,
	url.Parse,
}

type Todo struct{ CompletedAt *CompletedAt }

type TodoRow struct{ CompletedAt *time.Time }

type Session struct{ Token *private.Token }

type SessionRow struct{ Token *string }

type Page struct{ URL Website }

type Link struct{ URL *url.URL }

//kanna:mapper
type _ interface {
	// TodoFromRow lacks the error validated.New can return.
	TodoFromRow(TodoRow) Todo
	SessionFromRow(SessionRow) (Session, error)

	// PageToLink lacks the error url.Parse can return.
	PageToLink(Page) Link
}
