# hangdog

A watchdog for hung `go test` runs. It catches a stuck test **before** the
global timeout and prints **only the goroutines of the test that hung** — not a
wall of stacks from every package.

```
hangdog -- go test ./...
```

## The problem

A single test blocks forever — a channel that never receives, a lock that never
unlocks, an HTTP call with no timeout. In CI the usual outcomes are both bad:

- the global `go test -timeout` is left at its 10-minute default (or disabled),
  so the job burns ten minutes before failing; or
- when the timeout finally fires, you get a full goroutine dump of **every**
  running package, and you scroll through hundreds of runtime frames to find the
  one test that mattered.

## What hangdog adds

`go test -timeout` already prints the name of the timed-out test and a dump when
the **global** timeout fires. hangdog is not a replacement for that — it does the
two things the stock timeout does not:

1. **Fail fast.** Trigger on *silence* (no test activity for N seconds) or on a
   whole-run *budget*, long before a 10-minute global timeout — and independent
   of whether a timeout is set at all.
2. **Focused output.** Attribute the hang to a specific test and print only that
   test's goroutine and the goroutines it spawned. The other few hundred frames
   are dropped.

```
========================= HANGDOG =========================

HUNG: github.com/you/app/store.TestCheckout  [chan receive]  (trigger: idle 30s)

goroutine 34 [chan receive]:
github.com/you/app/store.TestCheckout(0x...)
	/app/store/checkout_test.go:88 +0x...
testing.tRunner(0x...)
	...
===========================================================
```

## Install

```
go install github.com/malikov73/hangdog/cmd/hangdog@latest
```

Unix only (Linux, macOS). The mechanism relies on `SIGQUIT`, which Windows does
not have.

## Usage

```
hangdog [flags] -- go test ./...
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `--idle` | `30s` | Fire when the test stream is silent this long while tests are still running (`0` disables). |
| `--budget` | `0` | Fire when the whole run exceeds this wall-clock duration (`0` disables). |
| `--hang-report` | — | Write the hang report to this file instead of stderr. |
| `--passthrough` | `false` | Stream raw `test2json` to stdout unchanged (for composition — see below). |

hangdog owns the timeout: it forces `-json`, sets `GOTRACEBACK=all`, and adds
`-timeout 0` unless you set a `-timeout` yourself.

### With gotestsum

hangdog composes with [gotestsum](https://github.com/gotestyourself/gotestsum)
via `--raw-command`. Keep the hang report in a file — gotestsum treats anything
on the wrapped command's stderr as an error:

```
gotestsum --raw-command -- hangdog --passthrough --hang-report hang.txt -- go test -json ./...
```

## How it works

1. hangdog runs `go test -json` and reads the `test2json` event stream,
   tracking which tests are in flight.
2. When the stream goes quiet past `--idle` (or the run exceeds `--budget`) while
   tests are still running, it finds the specific `<pkg>.test` child process of
   the stuck package (matched by working directory) and sends it `SIGQUIT`.
3. The Go runtime prints a full goroutine dump. hangdog parses it and reports the
   hung test.

The hung test's name is recovered from the **stack**, never from the `test2json`
`Test` field: on `SIGQUIT` the dump is tagged against whichever test was last
active, which is frequently one that already passed.

## Limitations

- **Unix only** — no `SIGQUIT` on Windows.
- With `-p >1` and several packages hanging at once, dump capture is best-effort;
  run stuck suites with `-p 1` for a guaranteed targeted dump.
- Attribution parses the runtime's goroutine-dump format, which is stable across
  Go releases but not contractual.

## Status

Early and evolving. Issues and feedback welcome.

## License

MIT — see [LICENSE](LICENSE).
