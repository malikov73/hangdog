// Package watch runs a wrapped `go test` invocation, watches its test2json
// stream, and — when a test stalls past the idle or budget trigger — delivers
// SIGQUIT to the stuck test binary and prints only the culprit's goroutines.
package watch

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/malikov73/hangdog/internal/dump"
	"github.com/malikov73/hangdog/internal/proc"
	"github.com/malikov73/hangdog/internal/testjson"
)

// Config controls a single hangdog run.
type Config struct {
	Idle        time.Duration // silence trigger: no stream activity for this long while tests run (0 disables)
	Budget      time.Duration // whole-suite budget: total wall-clock before triggering (0 disables)
	HangReport  string        // path to write the hang report to; empty => Stderr
	Passthrough bool          // stream raw test2json to Stdout unchanged (for gotestsum --raw-command)
	Stdout      io.Writer
	Stderr      io.Writer
}

// Run executes cmd (e.g. ["go","test","./..."]), supervises it, and returns the
// child's exit code. hangdog forces -json, GOTRACEBACK=all, and (unless the user
// set one) -timeout 0, so it is the sole timeout authority.
func Run(cfg Config, cmd []string) (int, error) {
	if len(cmd) == 0 {
		return 2, errors.New("no command to run")
	}
	if !proc.Supported {
		return 2, fmt.Errorf("hangdog is unsupported on %s: it relies on SIGQUIT; run `go test` directly", runtime.GOOS)
	}
	prepared := InjectFlags(cmd)

	c := exec.Command(prepared[0], prepared[1:]...)
	c.Env = append(os.Environ(), "GOTRACEBACK=all")
	c.Stderr = cfg.Stderr
	// Own process group: lets the watchdog terminate the whole test tree at once,
	// and keeps a CI cancel (SIGTERM to hangdog) from orphaning the child.
	proc.SetProcessGroup(c)
	stdout, err := c.StdoutPipe()
	if err != nil {
		return 1, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := c.Start(); err != nil {
		return 1, fmt.Errorf("start %q: %w", prepared[0], err)
	}
	pid := c.Process.Pid

	st := &state{
		cfg:     cfg,
		start:   time.Now(),
		last:    time.Now(),
		running: map[string]int{},
	}

	done := make(chan struct{})
	go st.watchdog(pid, done)
	stop := st.handleSignals(pid, done)
	defer stop()

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		var e testjson.Event
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		st.handle(line, e)
	}
	close(done)
	waitErr := c.Wait()

	st.report()

	exit := 0
	if c.ProcessState != nil {
		exit = c.ProcessState.ExitCode()
	}
	if exit < 0 {
		// The child was terminated by a signal (typically our own SIGKILL
		// escalation); ExitCode reports -1, which os.Exit would surface as 255.
		exit = 1
	}
	if exit == 0 && waitErr != nil {
		exit = 1
	}
	return exit, nil
}

// handleSignals force-terminates the whole test process group if hangdog itself is
// asked to stop (Ctrl-C, CI cancel, systemd stop). Because the child runs in its
// own process group it would otherwise be orphaned and — with the -timeout 0
// hangdog injected — leak forever. The returned func stops watching.
func (s *state) handleSignals(pgid int, done <-chan struct{}) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-done:
		case sig := <-ch:
			fmt.Fprintf(s.cfg.Stderr, "\n[hangdog] received %s; terminating the test run\n", sig)
			if err := proc.KillGroup(pgid); err != nil {
				fmt.Fprintf(s.cfg.Stderr, "[hangdog] kill process group %d: %v\n", pgid, err)
			}
		}
	}()
	return func() { signal.Stop(ch) }
}

type state struct {
	cfg   Config
	start time.Time

	mu        sync.Mutex
	last      time.Time
	running   map[string]int // package import path -> count of in-flight tests
	triggered bool
	trigger   string // human description of what fired
	capturing bool
	dumpLines []string
}

func (s *state) handle(raw []byte, e testjson.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last = time.Now()

	switch e.Action {
	case "run":
		if e.Test != "" {
			s.running[e.Package]++
		}
	case "pass", "fail", "skip":
		if e.Test != "" && s.running[e.Package] > 0 {
			s.running[e.Package]--
		}
	case "output":
		if s.capturing {
			// Buffer the runtime dump for the focused report.
			s.dumpLines = append(s.dumpLines, e.Output)
		}
	}

	if s.cfg.Passthrough {
		// gotestsum et al. consume the complete raw stream, dump included.
		s.cfg.Stdout.Write(raw)
		s.cfg.Stdout.Write([]byte("\n"))
		return
	}
	// Human mode: reconstruct normal `go test` text, but suppress the giant
	// post-trigger dump — the focused report replaces it.
	if e.Action == "output" && !s.capturing {
		io.WriteString(s.cfg.Stdout, e.Output)
	}
}

func (s *state) watchdog(pid int, done <-chan struct{}) {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			s.mu.Lock()
			if s.triggered {
				s.mu.Unlock()
				continue
			}
			reason := triggerReason(s.cfg, time.Since(s.last), time.Since(s.start), s.anyRunning())
			if reason == "" {
				s.mu.Unlock()
				continue
			}
			s.triggered = true
			s.trigger = reason
			s.capturing = true
			stuck := s.stuckPackages()
			s.mu.Unlock()

			fmt.Fprintf(s.cfg.Stderr, "\n[hangdog] %s exceeded; stuck package(s): %s\n", reason, strings.Join(stuck, " "))
			s.signal(pid, stuck)
			go s.escalate(pid, done)
		}
	}
}

// triggerReason decides whether the run should be interrupted, and why. The idle
// trigger is gated on a test being in flight so a slow build phase (which emits no
// test2json events) cannot false-positive; the budget trigger is pure wall-clock
// and fires unconditionally, so a hang in TestMain/init before any test starts is
// still caught. Idle takes precedence so the report names the silence, not the cap.
func triggerReason(cfg Config, idle, elapsed time.Duration, anyRunning bool) string {
	switch {
	case cfg.Idle > 0 && anyRunning && idle >= cfg.Idle:
		return fmt.Sprintf("idle %s", cfg.Idle)
	case cfg.Budget > 0 && elapsed >= cfg.Budget:
		return fmt.Sprintf("budget %s", cfg.Budget)
	}
	return ""
}

// graceKill is how long hangdog waits after the first trigger before force-killing
// the whole test process group. It is a var so tests can shorten it.
var graceKill = 15 * time.Second

// escalate guarantees the run ends. After a trigger, signal() delivers a targeted
// SIGQUIT to capture the culprit's dump; if the run has not ended graceKill later
// (the binary ignored SIGQUIT, could not be located, or another package is now
// hung), escalate SIGKILLs the entire process group so hangdog itself never blocks.
func (s *state) escalate(pgid int, done <-chan struct{}) {
	select {
	case <-done:
		// The run ended (the dump was flushed); nothing to escalate.
	case <-time.After(graceKill):
		fmt.Fprintf(s.cfg.Stderr, "[hangdog] run did not end %s after trigger; SIGKILL -> process group %d\n", graceKill, pgid)
		if err := proc.KillGroup(pgid); err != nil {
			fmt.Fprintf(s.cfg.Stderr, "[hangdog] kill process group %d: %v\n", pgid, err)
		}
	}
}

func (s *state) anyRunning() bool {
	for _, n := range s.running {
		if n > 0 {
			return true
		}
	}
	return false
}

func (s *state) stuckPackages() []string {
	var out []string
	for p, n := range s.running {
		if n > 0 {
			out = append(out, p)
		}
	}
	return out
}

// signal delivers SIGQUIT to the stuck packages' child test binaries so the
// runtime prints a goroutine dump, falling back to every discovered .test child
// when a package cannot be mapped. It is best-effort: escalate() is the guarantee
// that the run ends, so a discovery failure here only costs the focused dump, not
// termination.
func (s *state) signal(pid int, stuck []string) {
	bins, err := proc.TestBinaries(pid)
	if err != nil {
		fmt.Fprintf(s.cfg.Stderr, "[hangdog] cannot locate test binaries: %v; will force-kill the run\n", err)
		return
	}
	if len(bins) == 0 {
		fmt.Fprintln(s.cfg.Stderr, "[hangdog] no child .test binary found to signal; will force-kill the run")
		return
	}

	targets := matchTargets(bins, stuck)
	if len(targets) == 0 {
		targets = bins // fall back to all
	}
	for _, b := range targets {
		fmt.Fprintf(s.cfg.Stderr, "[hangdog] SIGQUIT -> %s (pid %d)\n", b.Name, b.PID)
		if err := proc.Quit(b.PID); err != nil {
			fmt.Fprintf(s.cfg.Stderr, "[hangdog] signal pid %d: %v\n", b.PID, err)
		}
	}
}

// matchTargets selects the test binaries whose working directory matches the
// source dir of a stuck package (resolved via `go list`).
func matchTargets(bins []proc.TestBinary, stuck []string) []proc.TestBinary {
	dirs := map[string]bool{}
	for _, pkg := range stuck {
		if d := pkgDir(pkg); d != "" {
			dirs[d] = true
		}
	}
	if len(dirs) == 0 {
		return nil
	}
	var out []proc.TestBinary
	for _, b := range bins {
		if b.Cwd != "" && dirs[b.Cwd] {
			out = append(out, b)
		}
	}
	return out
}

func pkgDir(importPath string) string {
	out, err := exec.Command("go", "list", "-f", "{{.Dir}}", importPath).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (s *state) report() {
	s.mu.Lock()
	text := strings.Join(s.dumpLines, "")
	triggered := s.triggered
	trigger := s.trigger
	s.mu.Unlock()

	if !triggered {
		return
	}

	w := s.cfg.Stderr
	if s.cfg.HangReport != "" {
		f, err := os.Create(s.cfg.HangReport)
		if err == nil {
			defer f.Close()
			w = f
		} else {
			fmt.Fprintf(s.cfg.Stderr, "[hangdog] cannot write %s: %v\n", s.cfg.HangReport, err)
		}
	}

	hangs := dump.HungTests(dump.Parse(text))
	fmt.Fprintf(w, "\n========================= HANGDOG =========================\n")
	if len(hangs) == 0 {
		fmt.Fprintf(w, "A hang fired (%s) but no test could be attributed from the dump.\n", trigger)
		if text == "" {
			fmt.Fprintln(w, "No goroutine dump was captured (is this `go test`? is SIGQUIT reaching the binary?).")
		}
		fmt.Fprintf(w, "===========================================================\n")
		return
	}
	for _, h := range hangs {
		fmt.Fprintf(w, "\nHUNG: %s  [%s]  (trigger: %s)\n\n", h.Name(), h.State, trigger)
		for _, g := range h.Stacks {
			fmt.Fprintf(w, "%s\n\n", g.Raw)
		}
	}
	fmt.Fprintf(w, "===========================================================\n")
}

// InjectFlags ensures the wrapped `go test` command emits test2json and lets
// hangdog own the timeout: it adds -json (if absent) and -timeout 0 (unless the
// user already set a -timeout), right after the `test` subcommand.
func InjectFlags(cmd []string) []string {
	testIdx := -1
	hasJSON := false
	hasTimeout := false
	for i, a := range cmd {
		switch {
		case a == "test" && testIdx == -1:
			testIdx = i
		case a == "-json" || a == "--json" || strings.HasPrefix(a, "-json="):
			hasJSON = true
		case a == "-timeout" || strings.HasPrefix(a, "-timeout=") || strings.HasPrefix(a, "--timeout"):
			hasTimeout = true
		}
	}
	if testIdx == -1 {
		return cmd // not a recognizable `go test`; run as given
	}

	var inject []string
	if !hasJSON {
		inject = append(inject, "-json")
	}
	if !hasTimeout {
		inject = append(inject, "-timeout", "0")
	}
	if len(inject) == 0 {
		return cmd
	}

	out := make([]string, 0, len(cmd)+len(inject))
	out = append(out, cmd[:testIdx+1]...)
	out = append(out, inject...)
	out = append(out, cmd[testIdx+1:]...)
	return out
}
