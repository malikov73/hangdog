// Package dump parses a Go runtime goroutine dump (as produced by SIGQUIT /
// GOTRACEBACK=all) and attributes a hang to the specific test that caused it.
//
// Attribution is derived entirely from the stack text, never from test2json's
// Test field: when the runtime dumps on SIGQUIT, `go test -json` tags the whole
// dump against whichever test was last active, which is regularly a test that
// already passed. The stack, by contrast, names the truth.
package dump

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	// Matches both the classic header ("goroutine 1 [chan receive]:") and the
	// modern runtime form that carries scheduler fields
	// ("goroutine 21 gp=0x.. m=nil [chan receive]:").
	headerRe = regexp.MustCompile(`^goroutine (\d+).*\[([^\]]*)\]:`)
	// A user test frame: <import/path>.TestXxx( — captures import path and test name.
	testFrameRe = regexp.MustCompile(`^(\S+)\.(Test\w+)\(`)
	createdByRe = regexp.MustCompile(`created by .* in goroutine (\d+)`)
)

// Goroutine is one parsed block of a runtime dump.
type Goroutine struct {
	ID        int
	State     string // e.g. "chan receive", "select", "IO wait"
	CreatedBy int    // parent goroutine ID, or 0 if unknown / created by main
	Raw       string // the full block text, verbatim
}

// Hang identifies a test that appears to be stuck, plus the goroutines that
// belong to it (its runner goroutine and everything it transitively spawned).
type Hang struct {
	Package string
	Test    string
	State   string
	Stacks  []Goroutine
}

// Name returns the "<package>.<Test>" identifier of the hang.
func (h Hang) Name() string { return h.Package + "." + h.Test }

// Parse splits a raw dump into goroutine blocks.
func Parse(text string) []Goroutine {
	var out []Goroutine
	var cur []string
	flush := func() {
		if len(cur) == 0 {
			return
		}
		block := strings.Join(cur, "\n")
		g := Goroutine{Raw: block}
		if m := headerRe.FindStringSubmatch(cur[0]); m != nil {
			g.ID, _ = strconv.Atoi(m[1])
			g.State = m[2]
		}
		if m := createdByRe.FindStringSubmatch(block); m != nil {
			g.CreatedBy, _ = strconv.Atoi(m[1])
		}
		out = append(out, g)
		cur = nil
	}
	for _, ln := range strings.Split(text, "\n") {
		if headerRe.MatchString(ln) {
			flush()
		}
		if len(cur) > 0 || headerRe.MatchString(ln) {
			cur = append(cur, ln)
		}
	}
	flush()
	return out
}

// HungTests attributes the dump to the test(s) responsible and returns each one
// together with its own goroutine and every goroutine it transitively created.
//
// A goroutine is a test root when it runs under testing.tRunner and contains a
// user TestXxx frame, but is not the harness's main goroutine (testing.runTests
// / testing.(*M).Run) and not TestMain.
func HungTests(gs []Goroutine) []Hang {
	byID := make(map[int]Goroutine, len(gs))
	childrenOf := make(map[int][]Goroutine, len(gs))
	for _, g := range gs {
		byID[g.ID] = g
		childrenOf[g.CreatedBy] = append(childrenOf[g.CreatedBy], g)
	}

	var hangs []Hang
	for _, g := range gs {
		if !strings.Contains(g.Raw, "testing.tRunner(") {
			continue
		}
		if strings.Contains(g.Raw, "testing.runTests") || strings.Contains(g.Raw, "testing.(*M).Run") {
			continue // the main harness goroutine, not a test
		}
		pkg, test := testFrame(g.Raw)
		if test == "" || test == "TestMain" {
			continue
		}
		hangs = append(hangs, Hang{
			Package: pkg,
			Test:    test,
			State:   g.State,
			Stacks:  collect(g, childrenOf),
		})
	}
	return hangs
}

// collect returns root plus all goroutines transitively created by it, in a
// stable BFS order rooted at the test goroutine.
func collect(root Goroutine, childrenOf map[int][]Goroutine) []Goroutine {
	var out []Goroutine
	seen := map[int]bool{}
	queue := []Goroutine{root}
	for len(queue) > 0 {
		g := queue[0]
		queue = queue[1:]
		if g.ID != 0 && seen[g.ID] {
			continue
		}
		seen[g.ID] = true
		out = append(out, g)
		if g.ID != 0 {
			queue = append(queue, childrenOf[g.ID]...)
		}
	}
	return out
}

func testFrame(block string) (pkg, test string) {
	for _, ln := range strings.Split(block, "\n") {
		if m := testFrameRe.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
			return m[1], m[2]
		}
	}
	return "", ""
}
