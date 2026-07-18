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
	// A user test frame: <import/path>.TestXxx( or FuzzXxx(, including subtest and
	// fuzz-seed closures like ".func1" / ".func1.2". Captures the import path and
	// the enclosing Test/Fuzz name, so a parallel subtest goroutine (whose frame is
	// "pkg.TestParent.func1(") is still attributed to TestParent.
	testFrameRe = regexp.MustCompile(`^(\S+)\.((?:Test|Fuzz)\w+)(?:\.func\d+(?:\.\d+)*)?\(`)
	// The "created by ... in goroutine N" trailer that names a goroutine's creator,
	// anchored to the start of a line so a file path containing those words cannot
	// masquerade as the trailer.
	createdByRe = regexp.MustCompile(`(?m)^created by .* in goroutine (\d+)`)
	// A method receiver, e.g. "(*HangSuite)" in "pkg.(*HangSuite).TestX", stripped
	// so a testify suite method's import path is not polluted with the receiver.
	receiverRe = regexp.MustCompile(`\.\(\*?[\w.]+\)$`)
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

// Parse splits a raw dump into goroutine blocks. A goroutine block ends at the
// next header or at a blank line, so the register dump and the test2json summary
// lines the runtime prints after the last goroutine are not folded into it.
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
		if ms := createdByRe.FindAllStringSubmatch(block, -1); ms != nil {
			// The genuine trailer is the block's last "created by" line.
			g.CreatedBy, _ = strconv.Atoi(ms[len(ms)-1][1])
		}
		out = append(out, g)
		cur = nil
	}
	for _, ln := range strings.Split(text, "\n") {
		if strings.TrimSpace(ln) == "" {
			flush() // blank line separates goroutine blocks from each other and from the register dump
			continue
		}
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
// user TestXxx/FuzzXxx frame, but is not the harness's main goroutine
// (testing.runTests / testing.(*M).Run) and not TestMain. A nested candidate — a
// subtest or suite method whose parent test also matched — is dropped so each hang
// is reported once, at its outermost test.
func HungTests(gs []Goroutine) []Hang {
	childrenOf := make(map[int][]Goroutine, len(gs))
	for _, g := range gs {
		childrenOf[g.CreatedBy] = append(childrenOf[g.CreatedBy], g)
	}

	type candidate struct {
		hang   Hang
		rootID int
		ids    map[int]bool // this candidate's own goroutine plus all it created
	}
	var cands []candidate
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
		stacks := collect(g, childrenOf)
		ids := make(map[int]bool, len(stacks))
		for _, s := range stacks {
			if s.ID != 0 {
				ids[s.ID] = true
			}
		}
		cands = append(cands, candidate{
			hang:   Hang{Package: pkg, Test: test, State: g.State, Stacks: stacks},
			rootID: g.ID,
			ids:    ids,
		})
	}

	var hangs []Hang
	for i, c := range cands {
		nested := false
		for j, other := range cands {
			if i != j && c.rootID != 0 && other.ids[c.rootID] {
				nested = true // c's root is inside another candidate's subtree
				break
			}
		}
		if !nested {
			hangs = append(hangs, c.hang)
		}
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

// testFrame returns the outermost Test/Fuzz frame in the block — the last one
// scanning top (innermost) to bottom (adjacent to testing.tRunner) — so a test
// that calls a Test-prefixed helper is attributed to the test the runner invoked,
// not to the helper. The receiver of a suite method is stripped from the package.
func testFrame(block string) (pkg, test string) {
	for _, ln := range strings.Split(block, "\n") {
		if m := testFrameRe.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
			pkg, test = receiverRe.ReplaceAllString(m[1], ""), m[2]
		}
	}
	return pkg, test
}
