# Phase 02 — `scribe init`

**Status: complete as plumbing. Not yet run against a real repo with a real writer.**

Five parallel units, one commit each: `internal/seed` (`e20362d`), `internal/install`
(`9399234`), `internal/replay` (`2a9a9e0`), `internal/wizard` (`371430c`), and the
`init` command wiring them together (`49c4d08`), all on `phase-02-init`.

---

## What shipped

| Package | Does |
|---|---|
| `internal/seed` | Scans README, package manifests, a pruned directory tree, and any existing `docs/scribe/*.md`; produces the writer prompt content for a first PROJECT.md/DECISIONS.md. Writes nothing itself |
| `internal/replay` | Finds this repo's past Claude Code transcripts under `~/.claude/projects/<mangled>/`, chunks them, and calls the writer once per chunk to produce CHANGELOG/JOURNAL entries. Resumable |
| `internal/install` | Installs the Stop hook into `.claude/settings.json`, reads/writes `.scribe/config.json` | 
| `internal/wizard` | huh-based interactive setup form, review screen, and progress reporting, all with safe no-TTY fallbacks |
| `cmd/scribe/init.go` | Ties the above together behind `scribe init`, dry-run by default |

## Design decisions worth knowing

**Dry run by default, `--apply` to make it real.** Everything `init` would write is
staged under `.scribe/init-preview/` (`initPreviewSubdir` in `cmd/scribe/init.go`) —
never under `docs/scribe/`. `--apply` copies the already-computed preview into the
real docs, installs the Stop hook, and writes config with `Enabled: true`. It does not
re-call the writer, so what an interactive operator approved in the review screen is
byte-for-byte what lands in the repo, and running dry-run-then-apply doesn't bill the
writer twice.

**Seed is capped and read-only.** `maxFileBytes = 32*1024` per file, `maxTotalFileBytes
= 256*1024` overall, `maxTreeEntries = 400`, directory walk depth-limited with noise
dirs (build output, vendor, etc.) pruned. `Parse` is strict: an empty or unparseable
writer response is an error, never silently-empty docs.

**Replay's transcript-directory mangling is verified empirically, not assumed.**
`internal/replay/replay.go`'s `mangle` reproduces the scheme Claude Code uses to name
`~/.claude/projects/<mangled>/`: every `/` and `.` in the absolute repo path becomes
`-`. The package doc says this was checked against real entries on disk. It's
undocumented anywhere upstream, so if the CLI changes the scheme, `FindSessions` just
stops finding sessions rather than misbehaving loudly.

**Replay is chunked and resumable.** Default 40 entries per chunk
(`defaultMaxEntriesPerChunk`), one writer call per chunk, checkpointed atomically to
`.scribe/replay.json`. A chunk is marked done only after the writer call, the parse,
and every `Emit` succeed. A corrupt state file degrades to starting over rather than
failing. A bad chunk doesn't abort the whole pass, but `Run` returns a non-nil error
naming every chunk that failed.

**Stop hook schema was checked against real docs and a real settings file.**
`hooks.Stop` is an array of matcher groups; `install.go` comments that `Stop` does not
support a `matcher` field and would silently ignore one, so scribe omits it entirely
rather than writing `"matcher": ""`. Install is idempotent by command *shape*, not
exact string match — `isScribeHookCommand` strips the trailing `hook` token and
compares `filepath.Base` of the binary path, so a scribe binary relocated to a
different path is still recognised rather than duplicated. Pre-existing settings are
backed up before any write, and all other keys/hook events round-trip as raw JSON.

**The wizard degrades safely with no TTY.** `IsInteractive` checks both stdin and
stdout against a real terminal; every exported function (`Ask`, `Review`, `Progress`)
returns `ErrNotInteractive` or falls back to plain stderr lines rather than hanging or
panicking. Defaults are `opencode` / `longcat-2.0-free`, carrying forward phase 00's
model-bakeoff recommendation. huh is pinned at v1.0.0.

---

## Verification

Gate, re-run just now:

```
$ gofmt -l .
(no output)

$ go vet ./...
(no output)

$ go build ./...
(no output)

$ go test -race -count=1 ./...
ok  	github.com/Sahil-796/scribe/cmd/scribe	6.524s
ok  	github.com/Sahil-796/scribe/internal/docs	2.748s
ok  	github.com/Sahil-796/scribe/internal/hook	1.811s
ok  	github.com/Sahil-796/scribe/internal/install	4.405s
ok  	github.com/Sahil-796/scribe/internal/queue	4.680s
ok  	github.com/Sahil-796/scribe/internal/replay	4.761s
ok  	github.com/Sahil-796/scribe/internal/seed	2.262s
ok  	github.com/Sahil-796/scribe/internal/transcript	3.927s
ok  	github.com/Sahil-796/scribe/internal/wizard	5.169s
ok  	github.com/Sahil-796/scribe/internal/worker	5.569s
ok  	github.com/Sahil-796/scribe/internal/writer	7.518s
```

(`internal/scribe` and `experiments/00-hook-probe` have no test files, as before.)

---

## What is NOT proven

**`scribe init` has never been run against a real repo with a real writer.** Every
test — `seed`, `replay`, `install`, `wizard`, and `cmd/scribe/init_test.go` — uses a
fake `scribe.Writer` and `t.TempDir()`. Same shape of gap as phase 01: the code is
tested, the behaviour is not demonstrated. Nobody has watched `init` produce a real
PROJECT.md from a real README, or real CHANGELOG entries from a real transcript.

**The wizard's interactive path is entirely untested.** There is no pty in this
environment. Unverified: actual huh form rendering, field navigation and validation
messages, the `huh.ErrUserAborted` (Ctrl+C/Esc) branch inside `Ask`/`Review`, the
interactive redraw branch of `Progress`, and `Ask`/`Review` returning the operator's
actual chosen values on the happy path. Only the not-interactive short-circuits
(`TestAsk_NotInteractive`, `TestReview_NotInteractive`, and the plain-line fallback in
`Progress`) are covered.

**Replay resumability is unverified at the `init` integration level.**
`internal/replay`'s own tests cover chunk-by-chunk resume directly.
`cmd/scribe/init_test.go` does not exercise it, because temp repos used in tests have no matching
`~/.claude/projects/` sessions on disk. An interrupted-then-resumed multi-chunk replay
has never run through `scribe init` end to end.

## Other gaps found while building this

- **The docs-dir wizard question is currently decorative.** `install.Config.DocsDir`
  is asked for, stored, and displayed, but `internal/docs.Store` hardcodes
  `scribe.DocsDir` (`docs/scribe`) regardless of what's configured. This matches locked
  Decision 9 ("fixed name, nothing to detect"), so it may be intentional — but as
  written, the wizard asks a question whose answer does nothing.
- **The empty-replay check is stringly-typed.** `init` detects "chunks processed but
  nothing emitted" by checking a `" (already done)"` suffix on `replay`'s
  human-readable progress labels (`cmd/scribe/init.go`, matched against
  `internal/replay/replay.go`'s `reportProgress` call). It works today but is coupled
  to a display string rather than a typed signal on `replay.Options`.
- **Onboarded repos get no `.gitignore` entries from `init`.** This repo's own
  `.gitignore` covers `.scribe/` and `docs/scribe/`, but `scribe init` doesn't add
  equivalent entries to the repo it's onboarding. Nobody decided what `init` should do
  here — leave it, warn, or write the entries.
- **`install` re-encodes `settings.json` through `encoding/json`, which alphabetises
  top-level keys.** `doc.marshal()` uses `json.MarshalIndent`. All keys and values
  survive, but original key order does not. Byte-exact preservation would need a JSON
  AST library, which isn't a dependency here.

## Next

1. Run `scribe init` live against a real repo with a real writer (`opencode` /
   `longcat-2.0-free`) and confirm the seed and replay output are usable, not just
   well-formed.
2. Decide the docs-dir question: drop it from the wizard, or make `internal/docs.Store`
   honour it.
3. Decide what `init` does about `.gitignore` in onboarded repos.
4. Carry forward phase 01's still-open items — the live loop run and the fail-open
   guard — neither was in scope here.
