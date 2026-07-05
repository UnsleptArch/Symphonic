# Writing a Symphonic plugin — quickstart

Full reference: [`../API.md`](../API.md). This page is just the fast
path to a working plugin.

## 1. Make a directory

```
plugins/my-plugin/
```

## 2. Write `plugin.json`

```json
{
  "name": "my-plugin",
  "entrypoint": "main.py",
  "hooks": ["after_tool:httpx"]
}
```

`entrypoint` must be a relative path inside this same directory — no
absolute paths, no `../` tricks. See "Containment" in `API.md` for why.

## 3. Write `main.py`

```python
#!/usr/bin/env python3
import sys, os
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "_sdk"))
from symphonic_plugin_sdk import load_context, Output

ctx = load_context()
out = Output()

# ctx.target, ctx.domain, ctx.output_dir are available here.
# ctx.results has every core Result so far.
# ctx.any_signal_contains("wordpress") is a quick way to check them.

out.add_result(tool="my-plugin", signals=["custom:whatever_you_found"])
out.emit()
```

Print your own debug output to `sys.stderr`, not `stdout` — stdout is
reserved for the final JSON `out.emit()` call.

## 4. Enable it in `conf.yaml`

```yaml
plugins_enabled: true
plugins:
  my-plugin: true
```

## 5. Run Symphonic normally

```
./symphonic
```

Your plugin's stderr output lands in
`output/<run>/plugin-my-plugin-after_tool_httpx.log`. Anything it
returned shows up in that run's `results.json` alongside the core
tools' entries.

## Worth knowing before you write anything real

- **No sandboxing.** Your plugin runs with your own full privileges —
  disk, network, subprocesses, everything. "Contained to the plugins
  folder" only means Symphonic won't launch a file outside your
  plugin's own directory; it says nothing about what that file is
  allowed to do once it's running.
- **No timeout is currently enforced.** A hanging plugin will hang the
  run at that point. Build your own timeout handling if that matters
  to you.
- **You're responsible for scope.** If your plugin reacts to something
  discovered mid-run by scanning something new, nothing currently stops
  it from scanning outside your original authorized target. See the
  "On scope" note in `API.md`.
- **Core will never include RCE-class or DDoS/load-class tooling.**
  This plugin system is exactly where that kind of thing is allowed to
  live instead — on your own recognizance, not reviewed or endorsed by
  the core project.

See `plugins/wp-flag/` for a small complete working example.
