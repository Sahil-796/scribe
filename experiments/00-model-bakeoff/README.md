# 00 — model bakeoff

The cleaned transcript this bakeoff was graded against is **deliberately not committed**.
It was a 924-line rendering of a real session from another private repo (`scout`) and
contained internal API routes, identifiers and architecture discussion that have no
business living in this repository's history.

To reproduce:

    python3 parser.py <path-to-a-real-transcript.jsonl> > cleaned.md

then feed `cleaned.md` plus `prompt.md` to each candidate model. The graded results and
the gate verdict are in `docs/findings/00-models.md`; the models' verbatim outputs are
under `outputs/`.

Treat transcripts as secrets. They contain whatever was on screen.
