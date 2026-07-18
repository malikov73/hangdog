package watch

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestInjectFlags(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "bare go test",
			in:   []string{"go", "test", "./..."},
			want: []string{"go", "test", "-json", "-timeout", "0", "./..."},
		},
		{
			name: "already has -json",
			in:   []string{"go", "test", "-json", "./..."},
			want: []string{"go", "test", "-timeout", "0", "-json", "./..."},
		},
		{
			name: "user set -timeout is respected",
			in:   []string{"go", "test", "-timeout", "5m", "./..."},
			want: []string{"go", "test", "-json", "-timeout", "5m", "./..."},
		},
		{
			name: "user set -timeout=form is respected",
			in:   []string{"go", "test", "-timeout=30s", "./..."},
			want: []string{"go", "test", "-json", "-timeout=30s", "./..."},
		},
		{
			name: "nothing to inject",
			in:   []string{"go", "test", "-json", "-timeout", "1m", "./..."},
			want: []string{"go", "test", "-json", "-timeout", "1m", "./..."},
		},
		{
			name: "flags preserved after packages",
			in:   []string{"go", "test", "./...", "-race", "-count=1"},
			want: []string{"go", "test", "-json", "-timeout", "0", "./...", "-race", "-count=1"},
		},
		{
			name: "not a go test command is untouched",
			in:   []string{"echo", "hello"},
			want: []string{"echo", "hello"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InjectFlags(tt.in)
			if !slices.Equal(got, tt.want) {
				t.Errorf("InjectFlags(%v)\n got = %v\nwant = %v", tt.in, got, tt.want)
			}
		})
	}
}

// Two packages that hang concurrently reuse the same goroutine IDs (18, 34).
// report() must parse each package's dump separately so package A's hang does not
// absorb package B's worker goroutine through a colliding created-by chain.
func TestReportGroupsByPackage(t *testing.T) {
	dumpFor := func(pkg, test, worker string) string {
		return "goroutine 18 [chan receive]:\n" +
			pkg + "." + test + "(0x0)\n\t/repo/" + pkg + "_test.go:8 +0x28\n" +
			"testing.tRunner(0x0, 0x0)\n\t/usr/local/go/src/testing/testing.go:2036 +0xc4\n" +
			"created by testing.(*T).Run in goroutine 1\n\t/usr/local/go/src/testing/testing.go:2101 +0x3a8\n\n" +
			"goroutine 34 [sleep]:\ntime.Sleep(0x1)\n\t/usr/local/go/src/runtime/time.go:363 +0x150\n" +
			pkg + "." + worker + "(0x0)\n\t/repo/" + pkg + "_test.go:20 +0x28\n" +
			"created by " + pkg + "." + test + " in goroutine 18\n\t/repo/" + pkg + "_test.go:7 +0x64\n"
	}
	var buf bytes.Buffer
	st := &state{
		cfg:       Config{Stderr: &buf},
		triggered: true,
		trigger:   "idle 2s",
		dumpByPkg: map[string][]string{
			"a": {dumpFor("a", "TestA", "workerA")},
			"b": {dumpFor("b", "TestB", "workerB")},
		},
	}
	st.report()
	out := buf.String()

	ai := strings.Index(out, "HUNG: a.TestA")
	bi := strings.Index(out, "HUNG: b.TestB")
	if ai < 0 || bi < 0 {
		t.Fatalf("both packages must be attributed:\n%s", out)
	}
	if strings.Contains(out[ai:bi], "workerB") {
		t.Errorf("package A's report cross-linked package B's goroutine:\n%s", out)
	}
	if strings.Contains(out[bi:], "workerA") {
		t.Errorf("package B's report cross-linked package A's goroutine:\n%s", out)
	}
}

func TestTriggerReason(t *testing.T) {
	const idle = 2 * time.Second
	const budget = 10 * time.Second
	tests := []struct {
		name       string
		cfg        Config
		idle       time.Duration
		elapsed    time.Duration
		anyRunning bool
		want       string
	}{
		{"idle fires while a test is in flight", Config{Idle: idle}, 3 * time.Second, 3 * time.Second, true, "idle 2s"},
		{"idle suppressed with no test in flight", Config{Idle: idle}, 3 * time.Second, 3 * time.Second, false, ""},
		{"budget fires with no test in flight", Config{Budget: budget}, 0, 11 * time.Second, false, "budget 10s"},
		{"budget fires regardless of the idle gate", Config{Budget: budget}, 0, 11 * time.Second, true, "budget 10s"},
		{"idle takes precedence when both would fire", Config{Idle: idle, Budget: budget}, 3 * time.Second, 11 * time.Second, true, "idle 2s"},
		{"budget wins when idle is gated out", Config{Idle: idle, Budget: budget}, 3 * time.Second, 11 * time.Second, false, "budget 10s"},
		{"idle disabled", Config{Idle: 0, Budget: budget}, 100 * time.Second, 5 * time.Second, true, ""},
		{"budget disabled and under idle", Config{Idle: idle, Budget: 0}, 1 * time.Second, 100 * time.Second, true, ""},
		{"neither configured", Config{}, 100 * time.Second, 100 * time.Second, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := triggerReason(tt.cfg, tt.idle, tt.elapsed, tt.anyRunning); got != tt.want {
				t.Errorf("triggerReason(%+v, %s, %s, anyRunning=%v) = %q, want %q",
					tt.cfg, tt.idle, tt.elapsed, tt.anyRunning, got, tt.want)
			}
		})
	}
}
