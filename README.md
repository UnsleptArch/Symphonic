# Symphonic

Symphonic orchestrates existing, trusted security tools (subfinder,
katana, httpx, ffuf, arjun, nuclei, dalfox, sqlmap) against a single
authorized target, sequentially, with structured output and a small
Python plugin system. It does not implement its own exploit or scanning
logic — it runs tools that already exist and makes them easier to run
together.

Current version: **v1.2** (state-awareness layer + signal extraction +
plugins). See [`API.md`](API.md) for the plugin API reference.

## What this is, and isn't

Symphonic is a **tool launcher with structured output**, not an
intelligence engine. It runs a fixed set of tools in a fixed order,
records what happened, and pulls a few basic signals out of three of
those tools' output. It does not yet:

- decide which tools to run based on earlier results (that's a future,
  not-yet-built version)
- correlate or deduplicate findings across tools
- score or rank findings by confidence
- do anything with RCE-class vulnerabilities beyond whatever the
  underlying tool itself reports at a detection level

Good for: authorized recon automation, learning how these tools
interact, lab/CTF targets, your own infrastructure. Not yet a
replacement for a structured manual pentest methodology.

## Hard boundaries (by design, not oversight)

- **Consent gate**: Symphonic refuses to run at all unless
  `consent_confirmed: true` is explicitly set in `conf.yaml`. This is a
  single boolean check, not a signed-scope system — it exists so nobody
  runs this by accident against a target they haven't thought about.
- **No RCE-class or DDoS/load-class tooling ships in core, ever.** If
  you want that, it belongs in an external plugin you write yourself —
  not this repo. See [`API.md`](API.md) for why plugins are unsandboxed
  by design.
- **No enforcement on flags.** `defaultFlags` in `tools.go` are sane
  starting points, not a ceiling — anything in `conf.yaml`'s `flags:`
  block replaces them entirely, no inspection or filtering. Same trust
  model as running these tools yourself directly.

This builds the `symphonic` binary, checks whether
subfinder/katana/httpx/ffuf/arjun/nuclei/dalfox/sqlmap are on your
PATH (and tells you how to get whichever ones aren't), and copies
`conf.example.yaml` to `conf.yaml` if one doesn't already exist.

No Go toolchain was available in the environment this was originally
written in, so run `go build .` yourself and confirm it actually
compiles before relying on it — everything here was written carefully
but not compile-checked.

## Configuring a run

Edit `conf.yaml`:

```yaml
consent_confirmed: false   # must be true — only against authorized targets
target: "https://example.com"
rate_limit_seconds: 2      # spacing between tool launches, not per-request

tools:
  subfinder: true
  katana: true
  httpx: true
  ffuf: true
  arjun: true
  nuclei: true
  dalfox: true
  sqlmap: false

flags:                     # optional, overrides defaultFlags per tool
  sqlmap: "-u {target} --batch --level=1 --risk=1"

allowed_domains: []        # scaffold only, not enforced by anything yet

plugins_enabled: false
plugins_dir: "plugins"
plugins:
  wp-flag: false
```

`{target}`, `{domain}`, and `{output}` get substituted into flag
strings at run time. `{domain}` strips scheme/path (used by subfinder).
`{output}` is currently only used by ffuf's default flags (its JSON
results go to a file via `-o`, not stdout).

## Running

```
./symphonic            # reads ./conf.yaml
./symphonic other.yaml  # or point at a different config file
```

Each run creates `output/<timestamp>/` containing:

- `<tool>.log` — raw stdout+stderr for each tool that ran
- `<tool>.json` — ffuf's structured results specifically (via `-o`)
- `results.json` — one structured `Result` per tool: exit code, log
  path, timestamp, and (for httpx/ffuf/nuclei) a `signals` list
- `plugin-<name>-<hook>.log` — stderr from any plugins that ran

## Tool coverage and what's deliberately excluded

| Tool | Role | Notes |
|---|---|---|
| subfinder | passive subdomain recon | domain-only, no active bruteforce |
| katana | crawl/recon | |
| httpx | live-host + tech fingerprint | JSON output, signals extracted |
| ffuf | endpoint/dir discovery | built-in wordlist, JSON output, signals extracted |
| arjun | hidden param discovery | detection only |
| nuclei | templated vuln/misconfig checks | curated tag allowlist by default, JSON output, signals extracted |
| dalfox | reflected XSS detection | `--skip-bav` — no blind-XSS callback infra |
| sqlmap | SQLi detection | `--level=1 --risk=1` by default; no structured output mode worth trusting, so no signal extraction for it |

Explicitly never integrated: load-testing/DoS tools (hping3 and
similar), C2 frameworks, anything from the RCE-exploitation category.
That's a hard line for what ships in this repo — see `API.md` for
where that kind of thing is allowed to live instead (an external
plugin, on your own recognizance).
