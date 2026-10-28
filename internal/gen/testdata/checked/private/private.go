// Package private declares a value type it keeps to itself: other packages can
// read a Token, but its constructor is unexported, so they cannot make one.
package private

import "github.com/go-kanna/value/rule"

// Token is token as other packages name it.
type Token = token

//kanna:value
type token struct{ s string }

func (token) validate(s string) error {
	if s == "" {
		return rule.Fail("token")
	}
	return nil
}
