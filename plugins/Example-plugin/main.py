#!/usr/bin/env python3
"""
wp-flag — example Symphonic plugin.

Fires after httpx. If httpx's signals mention "wordpress" anywhere,
emits its own Result flagging it more explicitly. Deliberately trivial —
this exists to demonstrate the plugin protocol end to end, not as a
real security check.
"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "_sdk"))
from symphonic_plugin_sdk import load_context, Output  # noqa: E402


def main():
    ctx = load_context()
    out = Output()

    if ctx.any_signal_contains("wordpress"):
        out.add_result(
            tool="wp-flag",
            signals=["custom:wordpress_confirmed_by_plugin"],
        )
    else:
        out.add_result(
            tool="wp-flag",
            signals=[],
        )

    out.emit()


if __name__ == "__main__":
    main()
