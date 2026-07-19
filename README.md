# hangdog

[![CI](https://github.com/malikov73/hangdog/actions/workflows/ci.yml/badge.svg)](https://github.com/malikov73/hangdog/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/malikov73/hangdog.svg)](https://pkg.go.dev/github.com/malikov73/hangdog)
[![Go Report Card](https://goreportcard.com/badge/github.com/malikov73/hangdog)](https://goreportcard.com/report/github.com/malikov73/hangdog)
[![Release](https://img.shields.io/github/v/release/malikov73/hangdog)](https://github.com/malikov73/hangdog/releases)

A watchdog for hung `go test` runs. It catches a stuck test **before** the global
timeout and prints **only the goroutines of the test that hung** — not a wall of
stacks from every package.

```
hangdog -- go test ./...
```

## The problem

A single test blocks forever — a channel that never receives, a lock that never
unlocks, an HTTP call with no timeout. In CI the usual outcomes are both bad:

- the global `go test -timeout` sits at its 10-minute default, so the job burns
  ten minutes before failing; and
- when the timeout finally fires, you get a goroutine dump of **every** running
  package and scroll through hundreds of runtime frames to find the test that
  mattered.

## What hangdog adds

`go test -timeout` already prints the name of the timed-out test and a dump when
the **global** timeout fires. hangdog is not a replacement for that — it does the
things the stock timeout does not:

1. **Fail fast.** Trigger on *silence* (no test activity for N seconds) or on a
   whole-run *budget*, long before a 10-minute global timeout — and independent
   of whether a timeout is set at all.
2. **Focused output.** Attribute the hang to a specific test — including subtests,
   `t.Parallel` cases, fuzz seeds and testify suites — and print only that test's
   goroutine and the ones it spawned. The other few hundred frames are dropped.
3. **Always terminates.** If the stuck binary can't be located or ignores
   `SIGQUIT`, hangdog force-kills the whole test process group after a grace
   period, so a run (or hangdog itself) never wedges.

```
$ hangdog --idle 5s -- go test ./...
=== RUN   TestCheckout

[hangdog] idle 5s exceeded; stuck package(s): github.com/you/app/store
[hangdog] SIGQUIT -> store.test (pid 85140)

========================= HANGDOG =========================

HUNG: github.com/you/app/store.TestCheckout  [chan receive]  (trigger: idle 5s)

goroutine 20 [chan receive]:
runtime.chanrecv1(...)
	/usr/local/go/src/runtime/chan.go:509
github.com/you/app/store.TestCheckout(...)
	/app/store/checkout_test.go:11
testing.tRunner(...)
	/usr/local/go/src/testing/testing.go:2036
created by testing.(*T).Run in goroutine 1

goroutine 21 [sleep]:
time.Sleep(...)
	/usr/local/go/src/runtime/time.go:363
github.com/you/app/store.chargeCard(...)          ← the goroutine the test spawned
	/app/store/checkout_test.go:15
created by github.com/you/app/store.TestCheckout in goroutine 20

===========================================================
```

## Install

```
go install github.com/malikov73/hangdog/cmd/hangdog@latest
```

Requires Go 1.23 or newer to build. No dependencies outside the standard library.

Unix only (Linux, macOS). The mechanism relies on `SIGQUIT`, which Windows does
not have; on an unsupported platform hangdog exits early rather than run without a
safety net.

## Usage

```
hangdog [flags] -- go test ./...
```

hangdog's own flags come **before** `--`; everything after `--` is executed
verbatim (with `-json` and `-timeout 0` injected into a recognized `go test`).

| Flag | Default | Meaning |
| --- | --- | --- |
| `--idle` | `30s` | Fire when the test stream is silent this long while tests are running (`0` disables). |
| `--budget` | `0` | Fire when the whole run exceeds this wall-clock duration (`0` disables). |
| `--hang-report` | — | Write the hang report to this file instead of stderr. |
| `--passthrough` | `false` | Stream raw `test2json` to stdout unchanged (for gotestsum — see below); pair with `--hang-report`. |
| `--version` | — | Print the version and exit. |

hangdog owns the timeout: it forces `-json`, sets `GOTRACEBACK=all`, and adds
`-timeout 0` unless you set a `-timeout` yourself. Disabling both `--idle` and
`--budget` is refused, so the run always has a safety net.

**Exit code:** hangdog passes through the wrapped command's exit code — `0` on a
clean run, non-zero when a test fails or a hang is caught. A usage error is `2`.

### In CI (GitHub Actions)

Wrap the test step and keep the report as an artifact:

```yaml
- name: Test
  run: go run github.com/malikov73/hangdog/cmd/hangdog@latest --idle 60s --hang-report hang.txt -- go test ./...
- name: Upload hang report
  if: failure()
  uses: actions/upload-artifact@v4
  with:
    name: hang-report
    path: hang.txt
```

### With gotestsum

hangdog composes with [gotestsum](https://github.com/gotestyourself/gotestsum) via
`--raw-command`. Write `-json` explicitly (gotestsum does not inject it) and keep
the hang report in a file — gotestsum treats any stderr from the wrapped command
as an error, and `--passthrough` routes hangdog's own messages into the report:

```
gotestsum --raw-command -- hangdog --passthrough --hang-report hang.txt -- go test -json ./...
```

`--rerun-fails` is not supported with this recipe: gotestsum reruns by appending
a `-test.run` filter and package to the raw command, which cannot narrow a wrapped
`go test ./...`.

## How it works

1. hangdog runs `go test -json` and reads the `test2json` event stream, tracking
   which tests are in flight.
2. When the stream goes quiet past `--idle` (or the run exceeds `--budget`), it
   finds the specific `<pkg>.test` child process of the stuck package (matched by
   working directory) and sends it `SIGQUIT`.
3. The Go runtime prints a goroutine dump. hangdog parses it, attributes the hang,
   and prints only the culprit's goroutines.

The hung test's name is recovered from the **stack**, never from the `test2json`
`Test` field: on `SIGQUIT` the dump is tagged against whichever test was last
active, which is frequently one that already passed.

## Limitations

- **Unix only** — no `SIGQUIT` on Windows.
- The trigger fires once, capturing every package stuck at that moment: a parallel
  run (the default) that hangs several packages at the same time prints a report
  for each. A package that only *starts* hanging later — e.g. under `-p 1`, where
  packages run one at a time — is not dumped individually; it is force-killed with
  the process group after the grace period.
- Attribution parses the runtime's goroutine-dump format, which is stable across
  Go releases but not contractual. CI exercises it on Go 1.23, oldstable and
  stable to catch drift.

## Status

Early and evolving (`v0.x`). Issues and feedback welcome.

## License

MIT — see [LICENSE](LICENSE).
