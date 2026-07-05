"""
symphonic_plugin_sdk.py

Import this from a plugin's main.py to talk to Symphonic without
hand-rolling the stdin/stdout JSON protocol yourself.

Minimal usage:

    from symphonic_plugin_sdk import load_context, Output

    ctx = load_context()
    # ctx.hook       -> e.g. "after_tool:httpx"
    # ctx.target     -> the target URL Symphonic is running against
    # ctx.domain     -> bare domain (scheme/path stripped)
    # ctx.output_dir -> this run's output directory
    # ctx.results    -> list of dicts, every core Result so far

    out = Output()
    out.add_result(
        tool="my-plugin",
        ran=True,
        exit_code=0,
        signals=["custom_check:something_interesting"],
    )
    out.emit()  # writes JSON to stdout — call this once, at the end

No sandboxing, no permission model, nothing enforced. This is exactly as
powerful as running the script yourself — use that power on targets
you're actually authorized to test, same rule as everything else in
this project.
"""

import json
import sys
import time
from dataclasses import dataclass, field
from typing import Any, Dict, List


@dataclass
class Context:
    hook: str
    target: str
    domain: str
    output_dir: str
    results: List[Dict[str, Any]] = field(default_factory=list)

    def signals_from(self, tool: str) -> List[str]:
        """Convenience: pull just one prior tool's signals list by name."""
        for r in self.results:
            if r.get("tool") == tool:
                return r.get("signals", []) or []
        return []

    def any_signal_contains(self, substring: str) -> bool:
        """Convenience: check every prior result's signals for a substring,
        e.g. any_signal_contains("wordpress") after httpx has run."""
        for r in self.results:
            for s in r.get("signals", []) or []:
                if substring.lower() in s.lower():
                    return True
        return False


def load_context() -> Context:
    """Reads and parses the JSON context Symphonic wrote to this
    process's stdin. Call this once, at the start of your plugin."""
    raw = sys.stdin.read()
    data = json.loads(raw)
    return Context(
        hook=data.get("hook", ""),
        target=data.get("target", ""),
        domain=data.get("domain", ""),
        output_dir=data.get("output_dir", ""),
        results=data.get("results", []) or [],
    )


class Output:
    """Accumulates results to hand back to Symphonic. Call emit() exactly
    once, after you're done — it writes the JSON blob Symphonic expects
    on stdout and nothing else should be printed to stdout by your
    plugin (use print(..., file=sys.stderr) for your own debug logging,
    which Symphonic captures to your plugin's own log file)."""

    def __init__(self):
        self._results: List[Dict[str, Any]] = []

    def add_result(
        self,
        tool: str,
        ran: bool = True,
        exit_code: int = 0,
        log_file: str = "",
        signals: List[str] = None,
        error: str = "",
    ) -> None:
        self._results.append(
            {
                "tool": tool,
                "target": "",  # filled in by caller if useful, optional
                "ran": ran,
                "exit_code": exit_code,
                "log_file": log_file,
                "timestamp": int(time.time()),
                "error": error,
                "signals": signals or [],
            }
        )

    def emit(self) -> None:
        payload = {"results": self._results}
        sys.stdout.write(json.dumps(payload))
        sys.stdout.flush()
