#!/usr/bin/env python3
"""Recompute from_byte/to_byte for a (session, from_line, to_line) triple.

Corpus items in corpus.json are pinned by both line numbers (for a human to
find and re-read the exchange) and byte offsets (what runner.go actually
slices out and hands to internal/transcript.Read). If a corpus session file
is ever touched, regenerate the byte offsets with this instead of
hand-editing them:

    python3 offsets.py <path-to-session.jsonl> <from_line> <to_line>

from_line/to_line are 1-indexed and inclusive. Prints "from_byte to_byte"
for pasting into corpus.json. Real Claude Code transcripts live under
~/.claude/projects/<mangled-repo-path>/<session-id>.jsonl.
"""
import sys


def offsets(path, start_line, end_line):
    off = 0
    from_b = None
    to_b = None
    with open(path, "rb") as f:
        for i, line in enumerate(f, 1):
            if i == start_line:
                from_b = off
            off += len(line)
            if i == end_line:
                to_b = off
                break
    return from_b, to_b


if __name__ == "__main__":
    if len(sys.argv) != 4:
        print(__doc__)
        sys.exit(1)
    path, s, e = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
    fb, tb = offsets(path, s, e)
    print(fb, tb)
