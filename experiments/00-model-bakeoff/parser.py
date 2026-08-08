#!/usr/bin/env python3
"""
Throwaway parser for Claude Code session transcripts (~/.claude/projects/*/*.jsonl).

Built for scribe phase 00 — the goal is a clean, human-readable rendering of
"what the human asked" and "what Claude said back", tool noise stripped, so a
cheap model (and a human grader) can work from readable text instead of raw
JSONL.

Empirically confirmed against a real transcript (see docs/findings/00-models.md
for evidence):

  1. Tool results come back as `type: "user"` messages whose `message.content`
     is a list of blocks, and at least one block has `type: "tool_result"`.
     These are NOT real user turns and must be filtered.

  2. Subagent (Task tool) turns carry `isSidechain: true`. Every record in the
     transcript we tested carries an `isSidechain` key (usually false); we did
     not find a single `true` instance in any locally available transcript
     (grepped every .jsonl under ~/.claude/projects — zero hits), because none
     of them happened to invoke the Task tool. The field is real and present
     on every message-type record, so filtering on it is cheap and safe even
     though we could not exercise the true-branch on real data. Handle it
     defensively regardless.

  3. NOT called out in the plan, found empirically: slash-command invocations
     (e.g. `/model claude-sonnet-5`) are also recorded as `type: "user"` with
     plain *string* content, wrapped in `<command-name>...</command-name>` or
     preceded by a `<local-command-caveat>` block. These are real entries but
     not narrative content a human said — we drop them from the cleaned
     rendering too (kept in stats as "command_invocations").

  4. Real human turns are `type: "user"` messages where `message.content` is a
     plain Python `str` (not a list). Real assistant replies are `type:
     "assistant"` messages where `message.content` is a list containing one
     or more `{"type": "text", ...}` blocks — those blocks, concatenated, are
     the reply. Assistant turns that contain only `tool_use`/`thinking` blocks
     and no `text` block are "silent" tool-orchestration steps with nothing to
     show a human — they're dropped from the cleaned rendering.

  5. Other block types seen in assistant content: `thinking` (extended
     thinking, dropped), `tool_use` (dropped — the tool_result pairs with it
     via user-turn filtering in rule 1).

  6. Non-message record types seen at the top level of the JSONL, all
     filtered out entirely: `custom-title`, `ai-title`, `mode`,
     `queue-operation`, `system` (hook bookkeeping, subtype
     `stop_hook_summary`), `attachment` (deferred-tool/agent-listing deltas —
     session metadata, not conversation), `last-prompt` (a redundant summary
     record duplicating the most recent user prompt).

  7. One more shape: a user message whose content is a list with a single
     `{"type": "text", "text": "[Request interrupted by user]"}` block (no
     tool_result). Not a real statement from the user — dropped, counted
     separately as "interruptions".

Usage:
    python3 parser.py <transcript.jsonl> [--out cleaned.md] [--stats]
"""
import json
import sys
import argparse
from pathlib import Path


def load_records(path):
    with open(path, "r", encoding="utf-8") as f:
        for lineno, line in enumerate(f, 1):
            line = line.strip()
            if not line:
                continue
            try:
                yield lineno, json.loads(line)
            except json.JSONDecodeError as e:
                print(f"WARN: line {lineno}: failed to parse JSON: {e}", file=sys.stderr)


def is_command_invocation(text):
    stripped = text.lstrip()
    return stripped.startswith("<command-name>") or stripped.startswith("<local-command-caveat>")


def is_interruption_only(content_list):
    if len(content_list) != 1:
        return False
    b = content_list[0]
    return isinstance(b, dict) and b.get("type") == "text" and b.get("text") == "[Request interrupted by user]"


def has_tool_result(content_list):
    return any(isinstance(b, dict) and b.get("type") == "tool_result" for b in content_list)


def extract_assistant_text(content_list):
    parts = []
    for b in content_list:
        if isinstance(b, dict) and b.get("type") == "text":
            t = b.get("text", "")
            if t.strip():
                parts.append(t)
    return "\n\n".join(parts).strip()


def parse_transcript(path):
    """Returns (turns, stats) where turns is a list of
    {"role": "user"|"assistant", "text": str, "line": int, "ts": str|None}
    in file order, and stats is a dict of counters."""
    stats = {
        "total_records": 0,
        "sidechain_filtered": 0,
        "non_message_records": 0,
        "tool_result_user_msgs": 0,
        "command_invocations": 0,
        "interruptions": 0,
        "silent_assistant_turns": 0,
        "real_user_turns": 0,
        "real_assistant_turns": 0,
        "raw_bytes": 0,
        "record_type_counts": {},
    }

    turns = []
    raw_bytes = Path(path).stat().st_size
    stats["raw_bytes"] = raw_bytes

    for lineno, rec in load_records(path):
        stats["total_records"] += 1
        rtype = rec.get("type")
        stats["record_type_counts"][rtype] = stats["record_type_counts"].get(rtype, 0) + 1

        if rec.get("isSidechain") is True:
            stats["sidechain_filtered"] += 1
            continue

        if rtype not in ("user", "assistant"):
            stats["non_message_records"] += 1
            continue

        msg = rec.get("message", {})
        content = msg.get("content")
        ts = rec.get("timestamp")

        if rtype == "user":
            if isinstance(content, str):
                if is_command_invocation(content):
                    stats["command_invocations"] += 1
                    continue
                turns.append({"role": "user", "text": content, "line": lineno, "ts": ts})
                stats["real_user_turns"] += 1
            elif isinstance(content, list):
                if has_tool_result(content):
                    stats["tool_result_user_msgs"] += 1
                    continue
                if is_interruption_only(content):
                    stats["interruptions"] += 1
                    continue
                # Unknown shape — surface it rather than silently dropping.
                text = extract_assistant_text(content)
                if text:
                    turns.append({"role": "user", "text": text, "line": lineno, "ts": ts})
                    stats["real_user_turns"] += 1
            continue

        if rtype == "assistant":
            if isinstance(content, list):
                text = extract_assistant_text(content)
                if text:
                    turns.append({"role": "assistant", "text": text, "line": lineno, "ts": ts})
                    stats["real_assistant_turns"] += 1
                else:
                    stats["silent_assistant_turns"] += 1
            continue

    return turns, stats


def render_markdown(turns):
    out = []
    for t in turns:
        who = "USER" if t["role"] == "user" else "ASSISTANT"
        ts = t["ts"] or ""
        out.append(f"### {who}  {ts}\n")
        out.append(t["text"].strip())
        out.append("\n")
    return "\n".join(out)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("transcript")
    ap.add_argument("--out", default=None, help="write cleaned markdown here")
    ap.add_argument("--stats", action="store_true", help="print stats as JSON")
    args = ap.parse_args()

    turns, stats = parse_transcript(args.transcript)
    cleaned = render_markdown(turns)
    stats["cleaned_bytes"] = len(cleaned.encode("utf-8"))
    stats["compression_ratio"] = round(stats["raw_bytes"] / max(stats["cleaned_bytes"], 1), 2)

    if args.out:
        Path(args.out).write_text(cleaned, encoding="utf-8")
        print(f"wrote {args.out} ({stats['cleaned_bytes']} bytes)", file=sys.stderr)
    else:
        print(cleaned)

    if args.stats:
        print(json.dumps(stats, indent=2), file=sys.stderr)


if __name__ == "__main__":
    main()
