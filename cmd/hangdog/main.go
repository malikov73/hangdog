// Command hangdog wraps `go test`, catches a hung test before the global
// timeout, and prints only the goroutines of the test that hung.
//
// Usage:
//
//	hangdog [flags] -- go test ./...
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"time"

	"github.com/malikov73/hangdog/internal/watch"
)

// version is overwritten at build time via -ldflags.
var version = "dev"

// resolveVersion returns the goreleaser-injected version, or falls back to the
// module version stamped into the build so `go install ...@latest` reports the
// real tag instead of "dev".
func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

func main() {
	idle := flag.Duration("idle", 30*time.Second, "trigger when the test stream is silent this long while tests are still running (0 disables)")
	budget := flag.Duration("budget", 0, "trigger when the whole run exceeds this wall-clock duration (0 disables)")
	hangReport := flag.String("hang-report", "", "write the hang report to this file instead of stderr")
	passthrough := flag.Bool("passthrough", false, "stream raw test2json to stdout unchanged (for gotestsum --raw-command); pair with --hang-report to keep stderr clean")
	showVersion := flag.Bool("version", false, "print version and exit")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "hangdog %s — a watchdog for hung `go test` runs\n\n", resolveVersion())
		fmt.Fprintf(os.Stderr, "Usage:\n  hangdog [flags] -- go test ./...\n\nFlags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nUnix only (Linux, macOS): the mechanism relies on SIGQUIT.\n")
	}
	flag.Parse()

	if *showVersion {
		fmt.Println(resolveVersion())
		return
	}

	cmd := flag.Args()
	if len(cmd) == 0 {
		flag.Usage()
		os.Exit(2)
	}

	if *idle <= 0 && *budget <= 0 {
		fmt.Fprintln(os.Stderr, "[hangdog] both --idle and --budget are disabled; refusing to strip go test's timeout (set --idle or --budget, or run go test directly)")
		os.Exit(2)
	}

	if *passthrough && *hangReport == "" {
		fmt.Fprintln(os.Stderr, "[hangdog] warning: --passthrough without --hang-report; the hang report goes to stderr, which gotestsum may treat as an error")
	}

	code, err := watch.Run(watch.Config{
		Idle:        *idle,
		Budget:      *budget,
		HangReport:  *hangReport,
		Passthrough: *passthrough,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
	}, cmd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[hangdog] %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
