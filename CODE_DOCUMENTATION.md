# Symphonic — Code Documentation

This document goes inside the implementation: what each function does
and *why* it was written that way rather than an obvious alternative.
If you just want to use Symphonic, read `README.md`. If you want to
write a plugin, read `API.md`. This is for someone about to modify the
Go source in `src/` itself.

Six files, each with one job:

| File | Job |
|---|---|
| `src/main.go` | Orchestration loop only — no parsing, no flag logic, no plugin logic lives here |
| `src/config.go` | `Config` struct + the hand-rolled `conf.yaml` reader |
| `src/tools.go` | Which tools exist, their default flags, how a flag string becomes an `exec.Cmd` |
| `src/signals.go` | Turning httpx/ffuf/nuclei's JSON output into short signal strings |
| `src/plugins.go` | Plugin manifest loading, entrypoint containment, the subprocess protocol |
| `src/results.go` | The `Result` struct and `results.json` writer |

Everything below was verified against a real build (`go build .` from
inside `src/`, Go 1.22, zero errors, `go vet ./...` clean) and a real
end-to-end run including an actual plugin invocation over stdin/stdout —
not just written and assumed correct.

---

## `config.go`

### Why a hand-rolled parser instead of a YAML library

`conf.yaml` is deliberately not parsed with a real YAML library
(`gopkg.in/yaml.v3` or similar). Two reasons:

- **Practical**: a YAML dependency means `go.mod`/`go.sum` entries and a
  module fetch at build time. For a tool meant to be simple to build,
  that's friction worth avoiding for a file format this small.
- **Philosophical**: `conf.yaml`'s actual shape is small and fixed —
  flat key/value pairs plus a few nested blocks. A real parser would
  handle far more syntax (anchors, multi-document files, block scalars)
  than this file will ever use.

The tradeoff, stated plainly: **this is not a general YAML parser.** It
understands exactly the shapes `conf.yaml` uses today. Add a new nested
structure that doesn't fit "top-level key: value" or "indented key:
value under a known section header," and this parser won't error — it
will just silently not populate the field. That's a real sharp edge to
know about before extending `conf.yaml`'s shape.

### How the section state machine works

`loadConfig` reads line by line with one `section` variable —
`""`, `"tools"`, `"flags"`, `"plugins"`, or `"allowed_domains"`. Per
line:

1. Skip blank lines and `#` comments.
2. If the trimmed line is exactly one of the four section headers, set
   `section` and move on.
3. If currently inside a section *and* the raw line is indented
   (two spaces or a tab), handle it as a member of that section —
   either a `"- value"` list item (`allowed_domains` only) or a
   `"key: value"` pair (everything else).
4. If a non-indented line appears while inside a section, that section
   has ended: reset `section = ""` and **fall through** to top-level
   key/value parsing for that same line, rather than skipping it.

Step 4's fall-through matters: a line like `target: "https://..."`
immediately after a `tools:` block needs to both end the `tools:`
section *and* be parsed as the top-level `target` key in the same pass.
If the code just reset `section` and `continue`d, that key would be
silently dropped.

### Why indentation checks the raw line, not the trimmed one

```go
isIndented := strings.HasPrefix(rawLine, "  ") || strings.HasPrefix(rawLine, "\t")
```

This has to run on `rawLine`, before whitespace is stripped — checking
`trimmed` for a leading space is always false by definition, since
`TrimSpace` already removed it. This is the one place in the parser
where "raw" vs "trimmed" is a control-flow decision, not cosmetic.

### `allowed_domains:` uses a different sub-grammar than the other three

`tools:`, `flags:`, `plugins:` all hold `key: value` pairs and share one
code path. `allowed_domains:` holds a YAML list (`- value` lines), so
it's checked first, before the shared key/value-splitting logic runs.
Forcing a list through the same "split on `:`" path would either need
awkward special-casing or silently mis-parse.

### Why `PluginsDir`'s default is applied after the scan loop

```go
if cfg.PluginsDir == "" {
    cfg.PluginsDir = "plugins"
}
```

This runs after parsing finishes, not as an initial struct value. If it
were an initial default and the config explicitly set
`plugins_dir: ""`, parsing would overwrite the default with that
explicit empty value — then this post-loop check resets it back to
`"plugins"` anyway. "Empty string" and "unset" end up indistinguishable
on purpose; there's no real use case for wanting an explicitly empty
plugins directory.

---

## `tools.go`

### Why `{target}`/`{domain}`/`{output}` are string substitution, not a templating engine

`buildCommand` does three `strings.ReplaceAll` calls, then
`strings.Fields` to split on whitespace. This is intentionally
unsophisticated — three fixed placeholder names with no conditionals
needed don't justify pulling in `text/template`.

**The real limitation, stated directly**: `strings.Fields` splits on
any whitespace with no quote-awareness. A target URL or a user-supplied
flag value containing a literal space gets split into two argv entries
instead of staying together. No escaping mechanism exists to work
around this. If this becomes a real problem, the fix is either a proper
shell-word-splitting library or moving to list-based flag config —
not attempted here because no current default or realistic target
triggers it.

### Why `{output}` exists as a separate placeholder from `{target}`

httpx and nuclei both write JSON lines straight to **stdout**, which
`main.go` already captures to `<tool>.log` via `cmd.Stdout = f`. No
extra plumbing needed — the log file already *is* the structured
output.

ffuf is different: `-of json` only controls output *format*; it
requires `-o <path>` to actually write it anywhere. Without `-o`, ffuf
sends human-readable output to stdout instead, defeating the point of
`-of json`. That path has to be known at command-build time and come
from `main.go` (which owns `outDir`), but has to land inside
`tools.go`'s flag string. `{output}` is the seam: `main.go` computes
`jsonOutPath := filepath.Join(outDir, tool+".json")` only for ffuf and
hands it into `buildCommand`, which substitutes it in.

A future tool needing a file-path flag reuses this same mechanism
rather than inventing a new placeholder — that's why the substitution
is generic (`strings.ReplaceAll(flagStr, "{output}", jsonOutPath)`)
instead of ffuf-specific string concatenation.

### Why `toolOrder` is a fixed slice, not runtime-configurable

The ordering is a safety property, not just a default: recon tools run
before confirm/vuln-scan tools so a future conditional-execution layer
could act on earlier signals. Making it user-reorderable would let
someone put sqlmap before recon, which breaks nothing *today* (nothing
conditions on order yet) but undermines the reasoning behind having an
order at all once that layer exists. It's a `var` rather than `const`
only because Go slices can't be consts — it's still meant to be
treated as fixed.

### Why `bareDomain` returns the input unchanged on failure, not an empty string

```go
func bareDomain(target string) string {
    u, err := url.Parse(target)
    if err != nil || u.Hostname() == "" {
        return target
    }
    return u.Hostname()
}
```

subfinder is the only consumer of `{domain}`. If `target` is malformed
enough that parsing fails, handing back the original string produces a
subfinder invocation that will either work anyway or fail loudly and
visibly in `subfinder.log`. An empty string would silently produce
`subfinder -d -silent` with no domain at all — a worse failure mode,
since it's harder to notice something went wrong.

---

## `signals.go`

### Why extraction is per-tool functions, not a generic JSON-path config

Each of `extractHTTPXSignals`, `extractFFUFSignals`,
`extractNucleiSignals` is hand-written against that tool's specific
JSON shape, rather than one generic config-driven extractor. A generic
version would be more flexible but is more indirection for real
overhead at only three tools. The threshold for revisiting this: if a
fourth or fifth tool's hand-written function starts looking repetitive
enough to hurt, that's the point to build the generic version — not
before there's evidence it's needed.

### Why parse errors are silently skipped, not logged or fatal

```go
if err := json.Unmarshal([]byte(line), &h); err != nil {
    continue // not JSON, or a version-mismatch field shape — skip rather than crash
}
```

A parse failure here could mean a genuinely malformed line, a blank
line that slipped past the earlier check, or — the one worth taking
seriously — the installed tool version renamed a JSON field since this
was written. Failing loudly on any of these would let one bad line kill
signal extraction for the whole tool run, worse than just getting fewer
signals. The real cost: if your installed version has renamed a field,
this fails *quietly* — you get partial or zero signals with nothing
telling you why. Flagged in this file's header comment for that exact
reason: check the JSON shape against your actual installed version if
signals come back empty.

### Why nuclei/httpx parse line-by-line but ffuf parses the whole file at once

httpx's `-json` and nuclei's `-jsonl` both produce **JSON Lines** — one
complete object per line, many lines per run. Parsing has to iterate
lines and unmarshal each independently.

ffuf's `-of json` output is **one JSON document** for the entire run,
with a `results` array nested inside. `extractFFUFSignals` unmarshals
the whole byte slice once into a struct with a `Results []struct{...}`
field — Go's decoder handles the array, no manual line splitting
involved.

Mixing these up (line-splitting ffuf's output, or treating httpx's
JSONL as one document) fails immediately — this is why the three
functions don't share a parsing helper despite similar signatures.

### Why signals are freeform colon-delimited strings, not structured objects

`"status:200"`, `"tech:wordpress"`, `"finding:<template-id>:<severity>"`
trade queryability for simplicity: nothing in v1.2 branches on signal
content yet (that's a future version's job), so there's no real
consumer whose needs should drive a stricter schema today. Locking in
structure before there's a consumer risks guessing wrong about what
shape that future logic actually needs. The plugin SDK's
`any_signal_contains()` does a substring check for the same reason —
it's working with the same freeform strings by the same design choice.

---

## `plugins.go`

### Why plugins are subprocesses, not an embedded interpreter

Two real options existed: embed a scripting language in the binary
(Lua via `gopher-lua`, no cgo needed), or run plugins as separate
processes over a wire protocol. Subprocess was chosen specifically so
plugin authors write **Python**, not Lua — the target audience already
uses Python for security tooling, and the sandboxing benefit an
embedded interpreter would offer isn't actually being used here (see
next section), so there's little upside to asking someone to learn a
second language for it. Embedding real Python in Go requires cgo and a
`libpython` runtime dependency, which breaks easy cross-compilation.
Subprocess-plus-JSON avoids that at the cost of per-call process-spawn
overhead, which is negligible at this scale.

### Why plugins are unsandboxed on purpose

Core Symphonic refuses to ship RCE-class or DDoS/load-class tooling,
and the plugin system is explicitly the release valve for anyone who
wants that capability anyway. Sandboxing plugins would narrow what they
could do, which would partially defeat the point of having an
unreviewed escape hatch at all. The decision: don't try to have it both
ways. Plugins get full privileges; the entire safety story is "only run
plugins you wrote or trust," identical to running any script yourself.

### How `resolveEntrypoint` actually prevents directory traversal

```go
absDir, err := filepath.Abs(pluginDir)
absEntry, err := filepath.Abs(filepath.Join(absDir, entrypoint))
rel, err := filepath.Rel(absDir, absEntry)
if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
    return "", fmt.Errorf("entrypoint escapes its plugin directory: %s", entrypoint)
}
```

Three steps:

1. Resolve the plugin's own directory to an absolute path.
2. Join that with whatever `entrypoint` the manifest provided, then
   resolve *that* to absolute too. `filepath.Join`/`filepath.Abs` clean
   `.`/`..` segments as part of resolution — this is what collapses
   something like `foo/../../etc/passwd` down to its real target,
   rather than leaving traversal syntax intact for a naive string check
   to miss.
3. Compute the entrypoint's path *relative to* the plugin directory. If
   it's genuinely inside, the relative path is a plain descendant with
   no leading `..`. If it escaped, the relative path starts with `..`
   or equals `..` exactly. Either is rejected.

**Why not `strings.HasPrefix(absEntry, absDir)`** — the more
naive-looking check: `/plugins/foo` is a string-prefix of
`/plugins/foo-evil/payload.py`, but `foo-evil` isn't a subdirectory of
`foo` at all. Using `filepath.Rel` checks the *semantic* filesystem
relationship instead of the string, avoiding that false-negative class
entirely. Worth remembering if this is ever refactored — the
string-prefix version looks simpler and is wrong in a way that's easy
to miss in review.

**What this does not defend against**: symlinks. If a plugin's
directory contains a symlink pointing outside `pluginDir`, and
`entrypoint` refers through it, `filepath.Abs` resolves the *lexical*
path, not necessarily the real target after symlink resolution,
depending on where the symlink sits. `os.Stat` at the end does follow
symlinks for existence checking, so a symlinked entrypoint would still
be found — but the containment check itself runs on the pre-symlink
path. This is a known, stated gap, not something silently handled.

### Why `EntrypointPath` is computed once in `loadPlugins`, not re-derived in `runPlugin`

Storing the already-validated absolute path on the struct means there's
exactly one place path-joining-plus-validation exists. If `runPlugin`
independently reconstructed the path from raw `Dir`+`Entrypoint`, a
future refactor could accidentally add a second path that skips
`resolveEntrypoint` entirely — not through malice, just through an
innocent "simplification." Having a field literally named
`EntrypointPath` sitting next to the raw `Entrypoint` field makes
bypassing it look obviously wrong in review.

### Why plugin stdout and stderr are handled completely differently

```go
var stdout bytes.Buffer
cmd.Stdout = &stdout
...
cmd.Stderr = logFile
```

stdout is buffered in memory because it's expected to contain exactly
one thing — the final JSON blob, parsed immediately after the process
exits. stderr goes to a log file on disk because it's expected to hold
arbitrary debug output of unknown length, written throughout execution,
matching how core tool output is already handled. This split is also
*why* the SDK documentation is emphatic that only the final
`out.emit()` call should touch stdout — any stray `print()` in a
plugin's own code lands in the same buffer `json.Unmarshal` expects to
be one valid document, and corrupts parsing.

### Why empty plugin stdout isn't treated as an error

```go
if stdout.Len() == 0 {
    return out, nil
}
```

A plugin might legitimately have nothing to report — e.g. one whose
whole purpose is writing its own file as a side effect. Forcing every
plugin to emit `{"results": []}` even when it has nothing to say is
pure boilerplate. The distinction still enforced: non-empty-but-invalid
JSON is still an error — the leniency is specifically for "nothing at
all," not "something, but broken."

---

## `results.go`

### Why `Signals` is initialized as `[]string{}`, not left nil

```go
func newResult(tool, target string) Result {
    return Result{ ..., Signals: []string{} }
}
```

Go's `json.Marshal` renders a nil slice as `null`, an empty slice as
`[]`. Since `Result.Signals` has no `omitempty` (a `Result` with no
signals is meaningfully different from a field that doesn't exist),
every `results.json` entry should consistently show `"signals": []`
rather than sometimes `null` depending on whether that tool's
extraction path happened to run. Consumers (a plugin reading
`ctx.results`, a future correlation pass) can then always safely
iterate `signals` without a null-check first.

### Why `results.json` is one file per run, not one per tool

Nothing in the codebase, or any plausible next version, needs one
tool's `Result` in isolation from the rest of that run's context. A
single array is also what a plugin or report generator would actually
want — "everything that happened this run" — rather than reconstructing
that from several files.

### Why plugin-contributed `Result`s share the same slice as core tools' `Result`s, with no source marker

A plugin's `Result` and httpx's `Result` are structurally identical and
live in the same list. This was a deliberate v1.2 simplification: the
`Tool` field already carries a name, and nothing downstream currently
filters "core only" vs "plugin only." If that distinction becomes
genuinely necessary later — say, a report wanting to visually separate
"known tool" findings from "third-party plugin" findings for trust
reasons — that's a schema addition to make then, not something silently
worked around now.

---

## `main.go` — how one full run actually flows

1. Parse an optional CLI arg for the config path, default `conf.yaml`.
2. `loadConfig` — any failure is immediately fatal, no partial-config
   fallback.
3. Consent gate check — fatal if false. This runs **before** plugin
   loading, deliberately: no plugin's `before_run` hook should ever get
   a chance to fire against an unauthorized target, even in principle.
4. Target-empty check, then rate-limit default (invalid/unset
   `rate_limit_seconds` warns and falls back to `2` rather than
   failing — a missing rate limit isn't as safety-critical as a missing
   consent flag, so it degrades instead of refusing to run).
5. Create the timestamped output directory once; every later write
   (tool logs, `results.json`, plugin logs) uses this same path.
6. If plugins are enabled, `loadPlugins` scans and validates manifests
   **once**, up front — not re-scanned per hook. A plugin directory
   added mid-run wouldn't be picked up until the next invocation.
7. `before_run` hooks fire with `pluginCtx.Results` still empty — no
   core tool has run yet, so there's nothing to report on.
8. Main loop over `toolOrder` (not over `cfg.Tools` directly — iterating
   the fixed slice and checking `cfg.Tools[tool]` inside the loop is
   what keeps execution order deterministic, since Go map iteration
   order is explicitly not guaranteed). Per tool:
   - Skip if disabled.
   - Build a fresh `Result`.
   - Compute `jsonOutPath` — non-empty only for ffuf.
   - `buildCommand`; a `nil` return (unknown tool — shouldn't happen
     given `toolOrder` is hardcoded, but handled defensively) records
     an error and moves on without running anything.
   - Wire both `Stdout` and `Stderr` to the tool's `.log` file — core
     tools don't get the stdout/stderr split treatment plugins get,
     since their structured output already comes via `-o`/`-jsonl`/
     `-json` flags into a separate channel, not by convention over
     which stream carries what.
   - Run it. Three outcomes distinguished: clean exit
     (`Ran=true, ExitCode=0`), non-zero exit via `*exec.ExitError`
     (`Ran=true`, real exit code recorded — many of these tools use
     exit codes for things other than "crashed," so non-zero isn't
     treated as a special failure), or failure to start at all (missing
     binary, permissions) — only that third case `continue`s past
     signal extraction and the `after_tool` hook entirely, since
     there's nothing to extract from or hook into.
   - Signal extraction, choosing the tool's log file or its separate
     JSON file depending on which tool it is.
   - Append the `Result`, update `pluginCtx.Results`, then immediately
     fire that tool's `after_tool:<name>` hooks — so a reacting plugin
     sees that tool's own result already in the list, not a stale copy
     from before it ran.
   - Sleep `rate_limit_seconds`, unless this was the last tool (no
     trailing sleep with nothing left to space out from).
9. `after_run` hooks fire with the complete result set, including
   everything every `after_tool` hook contributed along the way.
10. `writeResults` — failure here is a *warning*, not fatal; the actual
    scanning work is already done by this point, and losing the JSON
    summary isn't equivalent to the run itself failing.
11. Print the human-readable summary from the in-memory slice directly,
    not by re-reading `results.json` back off disk.

### Why plugin hook results feed into the same running slice consumed by later hooks

```go
pluginCtx.Results = results
results = append(results, runPluginsForHook("after_tool:"+tool, ...)...)
```

Whatever an `after_tool:httpx` plugin contributes becomes visible to
the *next* tool's `after_tool` hook and to `after_run`, because
`results` is one slice mutated across the whole loop, not reset per
tool. This means plugin ordering can matter: if two plugins both fire
on `after_tool:httpx` and the second depends on the first's
contribution being present, that works — but only because of this
accumulation, and only in whatever order `loadPlugins` read the
directory in, which is explicitly not a stable guarantee (directory
read order isn't fixed across filesystems/OSes).

---

## Known limitations, stated plainly

- No concurrency anywhere — tools and plugins run strictly
  sequentially. A deliberate v1 simplicity choice, not a ceiling
  that's been hit.
- No timeout enforcement on tool or plugin subprocesses. A hung
  `sqlmap` or a hung plugin blocks the entire run indefinitely.
- The conf.yaml parser silently ignores malformed nesting rather than
  erroring.
- Signal extraction fails silently on parse errors — a tool version
  bump that renames JSON fields produces no visible error, just
  fewer/no signals.
- `AllowedDomains` is parsed and completely unused by core — it exists
  only as a hook point for a future version or for a plugin to check
  manually.
- Plugin execution order is not guaranteed stable.
- Directory-traversal protection on plugin entrypoints does not
  explicitly account for symlink-based escapes.
- `strings.Fields`-based flag splitting has no quote-awareness — a flag
  value containing a literal space will split incorrectly.
