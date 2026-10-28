// Package private declares a validated value whose rule it keeps to itself, so
// that no other package can make one with validated.New.
package private

import "github.com/go-kanna/validated"

// Token holds a string tokenRule accepts.
type Token = validated.String[tokenRule]

type tokenRule struct{}

func (tokenRule) Validate(s string) error {
	if s == "" {
		return validated.Fail("token")
	}
	return nil
}

// Ticket's rule is a pointer, no more exported than tokenRule.
type Ticket = validated.String[*ticketRule]

type ticketRule struct{}

func (*ticketRule) Validate(s string) error {
	if s == "" {
		return validated.Fail("ticket")
	}
	return nil
}
