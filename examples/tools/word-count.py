#!/usr/bin/env python3
"""Tool: word-count — count words, lines, and characters in text.

Contract:
    stdin  → {"text": "hello world"}
    stdout → result text
    exit 0 → success
    exit 1 → error (stderr)

Usage with prism-bridge:
    prism-bridge tool --manifest examples/tools/word-count.json \
        --port 3002 -- python3 examples/tools/word-count.py
"""

import json
import sys


def main():
    data = json.load(sys.stdin)
    text = data.get("text", "")

    if not text:
        print("text is required", file=sys.stderr)
        sys.exit(1)

    lines = text.count("\n") + (1 if text and not text.endswith("\n") else 0)
    words = len(text.split())
    chars = len(text)

    print(f"Lines: {lines}\nWords: {words}\nCharacters: {chars}")


if __name__ == "__main__":
    main()
