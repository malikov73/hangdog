// Package testjson decodes the `go test -json` event stream (the format
// produced by `go tool test2json`).
package testjson

import (
	"bufio"
	"encoding/json"
	"io"
)

// Event is a single test2json record. Only the fields hangdog needs are kept.
//
// The Test field deliberately drives nothing in hangdog's attribution: on
// SIGQUIT the whole goroutine dump is emitted as Output events tagged against
// whichever test was last active, which is frequently NOT the hung one. The
// hung test's identity is recovered from the stack text instead (see internal/dump).
type Event struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Output  string  `json:"Output"`
	Elapsed float64 `json:"Elapsed"`
}

// Decode calls fn for every well-formed JSON event on r. Lines that fail to
// parse are skipped: `go test` may interleave non-JSON build output.
func Decode(r io.Reader, fn func(Event)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		fn(e)
	}
	return sc.Err()
}
