// Package pywarnings models Python's default warnings registry and the
// registry invalidation caused by warnings.catch_warnings.
package pywarnings

import (
	"fmt"
	"sync"
)

// Sink receives a warnings.warn record, with its Python caller location.
type Sink func(module string, line int, category, message, source string)

// Record is a captured Python warning.
type Record struct {
	Module, Category, Message, Source string
	Line                              int
}

var registry = struct {
	sync.Mutex
	seen     map[string]bool
	captures []*[]Record
}{seen: map[string]bool{}}

// Warn emits each (message, category, module, line) once per registry.
func Warn(sink Sink, module string, line int, category, message, source string) {
	key := fmt.Sprintf("%s\x00%d\x00%s\x00%s", module, line, category, message)
	registry.Lock()
	if n := len(registry.captures); n > 0 {
		out := registry.captures[n-1]
		*out = append(*out, Record{module, category, message, source, line})
		registry.Unlock()
		return
	}
	if registry.seen[key] {
		registry.Unlock()
		return
	}
	registry.seen[key] = true
	registry.Unlock()
	if sink != nil {
		sink(module, line, category, message, source)
	}
}

// Reset invalidates all caller registries when Python's warning filters change.
func Reset() {
	registry.Lock()
	registry.seen = map[string]bool{}
	registry.Unlock()
}

// Catch runs fn as warnings.catch_warnings(record=True). Entering and leaving
// the block invalidate registries, including those in other packages.
func Catch(fn func()) {
	Capture(fn)
}

// Capture returns the warnings raised in a catch_warnings(record=True)
// block with simplefilter("always"), including repeated warnings.
func Capture(fn func()) (records []Record) {
	registry.Lock()
	registry.seen = map[string]bool{}
	registry.captures = append(registry.captures, &records)
	registry.Unlock()
	defer func() {
		registry.Lock()
		registry.captures = registry.captures[:len(registry.captures)-1]
		registry.seen = map[string]bool{}
		registry.Unlock()
	}()
	fn()
	return records
}
