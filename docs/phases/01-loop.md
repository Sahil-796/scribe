# Phase 01 — The loop, one repo, one person

**Status: complete as plumbing. Not yet proven against a live writer.**

End to end and genuinely working, nothing configurable yet. Built as four parallel
units against a shared type contract in [`internal/scribe`](../../internal/scribe/types.go).

---

## What shipped

| Package | Does |
|---|---|
| `cmd/scribe` | Cobra command tree. `hook` works; `status`, `run`, `init`, `diff`, `on`/`off`, `doctor` are registered stubs naming the phase that owns them |
| `internal/hook` | Reads the payload on stdin, validates, resolves repo root, enqueues, exits |
| `internal/queue` | Append-only trigger queue, cross-process run lock, pending flag, coalescing |
| `internal/transcript` | Incremental JSONL reading with per-session byte offsets |
| `internal/docs` | The four docs — atomic writes, history that can't be truncated |
| `internal/writer` | Connector layer: `opencode` + `custom` escape hatch, registry for the rest |
| `internal/worker` | The run loop that ties it together |

## Design decisions worth knowing

**The hook never blocks.** `internal/hook` takes enqueue as a plain function value
rather than importing `internal/queue`, keeping the hot path's dependency graph
minimal and the package testable without touching disk. Repo root is found by walking
up from `cwd` looking for `.scribe`, the way git finds `.git`. No `.scribe` anywhere in
the ancestry means exit 0, silently — **off is the default everywhere** (decision 8).

**Enqueue never touches the run lock.** It appends under a microsecond-scale flock, so
a hook firing during a long writer run can't queue behind it.

**The run lock is a real cross-process lock** — an `O_EXCL` lockfile recording pid,
hostname and start time, not a Go mutex. Two `scribe` invocations are separate
processes; a mutex would have been decorative. Stale locks are reclaimed by pid
liveness plus an age backstop, so a killed worker or a reboot can't wedge a repo
forever.

**Offsets advance only after a successful write.** Reversing that ordering means a
crashed run silently loses a reply forever. Commented in detail in `runOnce` because
it's the kind of thing that looks like a harmless reorder later.

**History can't be collapsed by accident.** `WriteState` accepts only PROJECT and
DECISIONS; `AppendHistory` accepts only CHANGELOG and JOURNAL, and never truncates.
Each rejects the wrong doc kind. This matters more than usual: per decision 10, git is
the only undo and nothing auto-commits, so a bad write can destroy real work.

**Partial-line safety in the transcript reader.** The file is being appended to live
while we read it. Lines without a trailing newline are never parsed and the offset
never advances past them. Truncation and rotation reset to 0 rather than returning
garbage.

**Error policy is deliberately asymmetric.** A malformed transcript line is logged and
skipped. A corrupt *offset* file is fatal — silently resetting an offset would replay
content and duplicate doc entries, which is worse than stopping.

**Writer argv is isolated in one function** (`opencodeArgv`), because phase 00 was
still probing the real flags in parallel. Every connector runs with no TTY, a hard
timeout, and process-group `SIGKILL` so a wedged writer can't survive or leave orphans.

---

## Verification

Build, `go vet` and `gofmt` clean. Full suite green under `-race`:

```
ok  github.com/Sahil-796/scribe/cmd/scribe          4.333s
ok  github.com/Sahil-796/scribe/internal/docs       2.253s
ok  github.com/Sahil-796/scribe/internal/hook       2.620s
ok  github.com/Sahil-796/scribe/internal/queue      4.669s
ok  github.com/Sahil-796/scribe/internal/transcript (cached)
ok  github.com/Sahil-796/scribe/internal/worker     3.037s
ok  github.com/Sahil-796/scribe/internal/writer     7.154s
```

End-to-end against the real binary:

| Case | Result |
|---|---|
| Uninitialised repo | Silent no-op, exit 0 |
| Initialised repo | Trigger written to `queue.jsonl` with resolved root and timestamp |
| Malformed payload | `malformed payload: invalid character 'o'...`, exit 1 |
| Timing, 5 runs | **17–22 ms**, inside the 50 ms budget |

Notable tests: two-process lock contention via a genuinely spawned subprocess; stale
lock recovery by SIGKILLing the holder; 200-goroutine concurrent enqueue; torn trailing
write; offset does *not* advance when the writer or a doc write fails.

A real bug was caught by the queue's own tests: the first `SetPending` built temp
filenames from pid + `UnixNano()`, which collided under concurrency at macOS clock
resolution. Switched to `os.CreateTemp`. That would have surfaced months later as a
rare, unreproducible "scribe stopped updating".

---

## What is NOT proven

**Nothing has run `opencode` end to end and produced real docs in a real repo.** Every
test above uses fakes or stub executables. The loop is proven as plumbing.

This matters more than it normally would, because phase 00 found that opencode
**fails open**: without `--auto` it silently rejects the tool call and exits 0. So the
worker's "did the docs actually change" check is precisely the thing still unproven,
and exit 0 is not evidence of anything.

The phase's own done-when — *"work for an hour, touch nothing, and the four docs are
current afterwards"* — has **not** been demonstrated.

## Next

1. Run the loop live against `opencode` in a scratch repo. Confirm docs actually change.
2. Add the fail-open guard: treat "exit 0, no doc change" as a failure, surface it.
3. Feed phase 00's findings into the writer defaults — `longcat-2.0-free`, real argv.
4. Settle `isSidechain` and subagent Stop behaviour against real data before phase 03.
