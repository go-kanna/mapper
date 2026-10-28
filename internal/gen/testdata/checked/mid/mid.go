// Package mid holds a deep.Level, so that package checked sees package deep
// only through it.
package mid

import "github.com/go-kanna/mapper/internal/gen/testdata/checked/deep"

type Holder struct{ Level deep.Level }
