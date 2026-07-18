package dump

import (
	"sort"
	"testing"
)

// A realistic multi-goroutine dump: the harness main goroutine, the hung test,
// a worker the test spawned, and an unrelated runtime goroutine.
const sample = `goroutine 1 [chan receive]:
testing.(*T).Run(0xc0000f01a0, {0x10a2f40, 0x8}, 0x10c3a10)
	/usr/local/go/src/testing/testing.go:1750 +0x3ab
testing.runTests(0xc000100000, {0xc000012a80, 0x1, 0x1}, 0x0)
	/usr/local/go/src/testing/testing.go:2161 +0x8be
testing.(*M).Run(0xc000100000)
	/usr/local/go/src/testing/testing.go:2029 +0xf18
main.main()
	_testmain.go:59 +0x2ba

goroutine 20 [chan receive]:
github.com/malikov73/hangdog/testdata/hang.TestHang(0xc0000f0340)
	/repo/testdata/hang/hang_test.go:20 +0x30
testing.tRunner(0xc0000f0340, 0x10c3a10)
	/usr/local/go/src/testing/testing.go:1690 +0xf4
created by testing.(*T).Run in goroutine 1
	/usr/local/go/src/testing/testing.go:1743 +0x390

goroutine 34 [sleep]:
time.Sleep(0x3b9aca00)
	/usr/local/go/src/runtime/time.go:300 +0xe0
github.com/malikov73/hangdog/testdata/hang.worker(0x0)
	/repo/testdata/hang/hang_test.go:31 +0x50
created by github.com/malikov73/hangdog/testdata/hang.TestHang in goroutine 20
	/repo/testdata/hang/hang_test.go:18 +0x64

goroutine 5 [syscall]:
os/signal.signal_recv()
	/usr/local/go/src/runtime/sigqueue.go:152 +0x29
created by os/signal.Notify.func1.1 in goroutine 1
	/usr/local/go/src/os/signal/signal.go:151 +0x1f
`

func TestParseIDsAndParents(t *testing.T) {
	gs := Parse(sample)
	if len(gs) != 4 {
		t.Fatalf("got %d goroutines, want 4", len(gs))
	}
	got := map[int]int{} // id -> createdBy
	for _, g := range gs {
		got[g.ID] = g.CreatedBy
	}
	want := map[int]int{1: 0, 20: 1, 34: 20, 5: 1}
	for id, parent := range want {
		if got[id] != parent {
			t.Errorf("goroutine %d createdBy = %d, want %d", id, got[id], parent)
		}
	}
}

func TestHungTestsAttribution(t *testing.T) {
	hangs := HungTests(Parse(sample))
	if len(hangs) != 1 {
		t.Fatalf("got %d hangs, want 1: %+v", len(hangs), hangs)
	}
	h := hangs[0]
	if h.Package != "github.com/malikov73/hangdog/testdata/hang" {
		t.Errorf("package = %q", h.Package)
	}
	if h.Test != "TestHang" {
		t.Errorf("test = %q, want TestHang", h.Test)
	}
	if h.State != "chan receive" {
		t.Errorf("state = %q, want chan receive", h.State)
	}

	var ids []int
	for _, g := range h.Stacks {
		ids = append(ids, g.ID)
	}
	sort.Ints(ids)
	// The test goroutine (20) plus the worker it created (34) — not main (1) or
	// the unrelated signal goroutine (5).
	if len(ids) != 2 || ids[0] != 20 || ids[1] != 34 {
		t.Errorf("filtered goroutine IDs = %v, want [20 34]", ids)
	}
}

// Modern Go runtime headers carry scheduler fields (gp=/m=/mp=) between the id
// and the [state]. Parsing must handle them, not just the classic form.
func TestParseModernHeader(t *testing.T) {
	const modern = `goroutine 21 gp=0x51e207ee2248 m=nil [chan receive]:
github.com/malikov73/hangdog/testdata/hang.TestHang(0x51e207ee2248)
	/repo/testdata/hang/hang_test.go:25 +0x30
testing.tRunner(0x51e207ee2248, 0x1045ccc68)
	/usr/local/go/src/testing/testing.go:2036 +0xc4
created by testing.(*T).Run in goroutine 1
	/usr/local/go/src/testing/testing.go:2101 +0x3a8
`
	gs := Parse(modern)
	if len(gs) != 1 || gs[0].ID != 21 || gs[0].State != "chan receive" {
		t.Fatalf("modern header parse failed: %+v", gs)
	}
	hangs := HungTests(gs)
	if len(hangs) != 1 || hangs[0].Test != "TestHang" {
		t.Fatalf("modern header attribution failed: %+v", hangs)
	}
}

// The dump's test2json Test field would blame the last-active test; attribution
// must ignore it. Here a passed test's frame must never be picked as the hang.
func TestHungTestsIgnoresPassedTests(t *testing.T) {
	const passed = `goroutine 8 [runnable]:
github.com/x/y.TestFast(0xc0000f0000)
	/repo/y_test.go:10 +0x20
testing.tRunner(0xc0000f0000, 0x10c3a10)
	/usr/local/go/src/testing/testing.go:1690 +0xf4
created by testing.(*T).Run in goroutine 1
	/usr/local/go/src/testing/testing.go:1743 +0x390
`
	// A runnable (not blocked) test is still reported structurally; the point of
	// this test is that HungTests keys off stack frames, so it finds TestFast by
	// name from the stack, proving JSON's Test field is never consulted.
	hangs := HungTests(Parse(passed))
	if len(hangs) != 1 || hangs[0].Test != "TestFast" {
		t.Fatalf("expected TestFast from stack parsing, got %+v", hangs)
	}
}
