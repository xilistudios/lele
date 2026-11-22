package tui

import (
	"io"
	"sync"
)

const (
	autowrapOffSequence = "\x1b[?7l"
	autowrapOnSequence  = "\x1b[?7h"
)

func DisableAutowrap(out io.Writer) (restore func()) {
	if out == nil {
		return func() {}
	}
	_, _ = io.WriteString(out, autowrapOffSequence)

	var once sync.Once
	return func() {
		once.Do(func() {
			_, _ = io.WriteString(out, autowrapOnSequence)
		})
	}
}
