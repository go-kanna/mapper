// Package checked maps to and from value types of kanna-value. Its
// value_gen.go, and those of the packages private, deep, and emit/model, are
// kanna-value's output, written from the root of the module by
//
//	go run github.com/go-kanna/value/cmd/kanna-value@v0.0.1 \
//		./internal/gen/testdata/checked ./internal/gen/testdata/checked/private \
//		./internal/gen/testdata/checked/deep ./internal/gen/testdata/emit/model
package checked

import (
	"net/url"
	"time"

	"github.com/go-kanna/value/rule"

	"github.com/go-kanna/mapper/internal/gen/testdata/checked/mid"
	"github.com/go-kanna/mapper/internal/gen/testdata/checked/private"
)

// CompletedAt holds a time from 2000 on.
//
//kanna:value
type CompletedAt struct{ t time.Time }

func (CompletedAt) validate(t time.Time) error {
	if t.Year() < 2000 {
		return rule.Fail("completed_at")
	}
	return nil
}

// Count is unexported as count, and so is its constructor, which this package
// can still call.
type Count = count

//kanna:value
type count struct{ n int64 }

func (count) validate(n int64) error {
	if n < 0 {
		return rule.Fail("count")
	}
	return nil
}

//kanna:value
type Email struct{ s string }

func (Email) validate(s string) error { return rule.Email(s) }

//kanna:value
type Website struct{ s string }

func (Website) validate(s string) error { return rule.URL(s) }

// Odd has the shape of a value type, but its constructor cannot fail, as
// kanna-value's always can: an Odd is read with its accessor, but not made.
type Odd struct{ n int }

func (o Odd) Int() int { return o.n }

func NewOdd(n int) Odd { return Odd{n: n} }

// Pair holds a slice, after which no accessor is named, so it is no value type.
type Pair struct{ s []string }

func (p Pair) Strings() []string { return p.s }

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
// NewEmail.
func parseEmail(s string) (Email, error) { return NewEmail(s) }

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

type OddRow struct{ N int }

type LevelRow struct{ Level int }

type Odds struct{ N Odd }

type Page struct{ URL Website }

type Link struct{ URL *url.URL }

//kanna:mapper
type _ interface {
	// TodoFromRow lacks the error NewCompletedAt can return.
	TodoFromRow(TodoRow) Todo
	SessionFromRow(SessionRow) (Session, error)

	// PageToLink lacks the error url.Parse can return.
	PageToLink(Page) Link

	OddsFromRow(OddRow) (Odds, error)
	HolderFromRow(LevelRow) (mid.Holder, error)
}
