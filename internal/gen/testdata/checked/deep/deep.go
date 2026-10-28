// Package deep declares a value type that package checked reaches only
// through package mid, and so sees without its constructor.
package deep

//kanna:value
type Level struct{ n int }

func (Level) validate(int) error { return nil }
