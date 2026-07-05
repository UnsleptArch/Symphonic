# Symphonic Plugin API Reference

This is the full reference for writing a Symphonic plugin. For a
quicker "just get something running" version, see
[`plugins/README.md`](plugins/README.md).

## What a plugin is

A plugin is a Python script, invoked as its own subprocess, that
receives a JSON blob on stdin describing the current run and can hand
back additional `Result` entries on stdout. It has no special
privileges and no restrictions beyond one thing: **the file Symphonic
executes must live inside the plugin's own directory** (see
[Containment](#containment) below). Beyond that, a plugin can do
anything a normal Python script running on your machine can do —
network calls, filesystem access, spawn other processes, whatever.
Nothing about the plugin system sandboxes plugin *behavior*.

This is intentional, not an oversight. Core Symphonic will never ship
or maintain RCE-class or DDoS/load-class tooling in-tree. If you want
that, this is where it goes — as a plugin you wrote and are responsible
for, not something reviewed or endorsed by the core project. Only load
plugins you wrote or fully trust; a plugin runs with exactly your own
privileges.

## Directory layout

```
plugins/
└── my-plugin/
    ├── plugin.json     # required
    └── main.py         # or whatever you named it in plugin.json
```

## `plugin.json` schema

```json
{
  "name": "my-plugin",
  "entrypoint": "main.py",
  "hooks": ["after_tool:httpx"]
}
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | string | no | Defaults to the directory name if omitted. This is also the key you enable under `plugins:` in `conf.yaml`. |
| `entrypoint` | string | **yes** | Relative path to the file to execute, relative to the plugin's own directory. Must not be absolute and must not use `..` to climb outside the directory — see [Containment](#containment). |
| `hooks` | array of strings | **yes** | Which hook(s) this plugin fires on. See [Hooks](#hooks). |

## Containment

`entrypoint` is validated at load time, before anything runs:

- Absolute paths are rejected.
- Paths that resolve (via `filepath.Abs` + `filepath.Rel`) to anywhere
  outside the plugin's own directory are rejected — this blocks `../`
  traversal tricks.
- The resolved file must actually exist.

A plugin failing this check is skipped with a warning; it does not
crash the run, and it does not get a chance to execute at all.

**What this does and doesn't mean:** this constrains *which file* gets
launched. It does not sandbox *what that file does* once it's running.
Once `python3 <entrypoint>` starts, it has your full privileges. Don't
read "contained to the plugins folder" as "sandboxed" — those are
different guarantees.

## Enabling a plugin

In `conf.yaml`:

```yaml
plugins_enabled: true
plugins_dir: "plugins"
plugins:
  my-plugin: true
```

Plugins are opt-in per name, same pattern as the `tools:` block. A
plugin with a valid manifest that isn't explicitly set to `true` here
simply never runs.

## Hooks

| Hook | Fires | Typical use |
|---|---|---|
| `before_run` | Once, before any core tool starts | Custom pre-checks, your own recon step, a scope-enforcement plugin (see note below) |
| `after_tool:<name>` | After a specific core tool finishes (e.g. `after_tool:httpx`) | React to that tool's signals — e.g. "if WordPress detected, do X" |
| `after_run` | Once, after every core tool has finished | Custom reporting, correlation across the full result set |

`<name>` in `after_tool:<name>` must exactly match one of: `subfinder`,
`katana`, `httpx`, `ffuf`, `arjun`, `nuclei`, `dalfox`, `sqlmap`.

**On scope:** nothing in the current hook system automatically limits
what a plugin does with what it learns. If you write a plugin that
reacts to a discovered subdomain or endpoint by launching further
scans, that's on you to scope correctly — `conf.yaml`'s
`allowed_domains:` field exists (currently unenforced by core) as a
place a plugin *could* check before acting, if you want to build that
check yourself.

## Protocol

1. Symphonic builds a `PluginContext` and marshals it to JSON.
2. Symphonic runs `python3 <entrypoint>`, writes that JSON to the
   subprocess's stdin, and closes stdin.
3. The plugin reads stdin, does whatever it does, and writes a JSON
   `PluginOutput` object to stdout before exiting.
4. Anything the plugin writes to **stderr** is captured to
   `output/<run>/plugin-<name>-<hook>.log`. Anything printed to
   **stdout** other than the final JSON output will break parsing —
   use stderr for your own debug logging.
5. If the plugin's stdout is empty, Symphonic treats that as "no
   additional results," not an error — a plugin with pure side effects
   and nothing to report back is fine.

### `PluginContext` (stdin, JSON)

```json
{
  "hook": "after_tool:httpx",
  "target": "https://example.com",
  "domain": "example.com",
  "output_dir": "output/20260704-172200",
  "results": [
    {
      "tool": "httpx",
      "target": "https://example.com",
      "ran": true,
      "exit_code": 0,
      "log_file": "output/20260704-172200/httpx.log",
      "timestamp": 1751654520,
      "signals": ["status:200", "tech:wordpress"]
    }
  ]
}
```

| Field | Type | Description |
|---|---|---|
| `hook` | string | The exact hook string that triggered this run. |
| `target` | string | The full target as set in `conf.yaml`. |
| `domain` | string | Bare domain, scheme/path stripped. |
| `output_dir` | string | This run's output directory — write your own log/output files here if you want them alongside core tool output. |
| `results` | array | Every `Result` recorded so far this run, in order. |

### `Result` shape (used both in `results` above and in your output)

| Field | Type | Notes |
|---|---|---|
| `tool` | string | Tool or plugin name. |
| `target` | string | Optional — fine to leave blank from a plugin. |
| `ran` | bool | |
| `exit_code` | int | |
| `log_file` | string | Optional. |
| `timestamp` | int (unix seconds) | |
| `error` | string | Optional, omit if empty. |
| `signals` | array of strings | Freeform. Existing convention from core: `"status:200"`, `"tech:wordpress"`, `"endpoint_found:/admin:200"`, `"finding:<template-id>:<severity>"`. Not enforced — plugins can invent their own signal vocabulary. |

### `PluginOutput` (stdout, JSON) — what you write back

```json
{
  "results": [
    {
      "tool": "my-plugin",
      "ran": true,
      "exit_code": 0,
      "signals": ["custom:something_interesting"]
    }
  ]
}
```

Only `results` is read. Everything in each entry follows the `Result`
shape above.

## Python SDK

`plugins/_sdk/symphonic_plugin_sdk.py` wraps the protocol above so you
don't hand-roll JSON parsing. Import it like this from your plugin:

```python
import sys, os
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "_sdk"))
from symphonic_plugin_sdk import load_context, Output
```

### `load_context() -> Context`

Reads and parses stdin once. Returns a `Context` with:

- `.hook`, `.target`, `.domain`, `.output_dir` — direct fields
- `.results` — the raw list of result dicts
- `.signals_from(tool: str) -> list[str]` — convenience lookup for one
  prior tool's signals by name
- `.any_signal_contains(substring: str) -> bool` — case-insensitive
  substring check across every prior result's signals; useful for
  quick "did anything mention X" checks

### `Output`

Accumulates results and emits them once at the end.

- `.add_result(tool, ran=True, exit_code=0, log_file="", signals=None, error="")`
- `.emit()` — writes the final JSON to stdout. Call this exactly once,
  as the last thing your plugin does. Don't print anything else to
  stdout.

### Minimal example

```python
#!/usr/bin/env python3
import sys, os
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "_sdk"))
from symphonic_plugin_sdk import load_context, Output

ctx = load_context()
out = Output()

if ctx.any_signal_contains("wordpress"):
    out.add_result(tool="my-plugin", signals=["custom:wordpress_confirmed"])
else:
    out.add_result(tool="my-plugin", signals=[])

out.emit()
```

See `plugins/wp-flag/` in this repo for a complete working version of
exactly this example.

## Failure handling

- A plugin that exits non-zero, times out (no timeout is currently
  enforced — worth knowing if you write something that could hang),
  or writes malformed JSON to stdout has its error logged to stderr on
  the Symphonic process and is skipped for that hook. It does not stop
  other plugins or core tools from running.
- A plugin manifest that fails containment validation or JSON parsing
  is skipped at load time, before any hook fires.
