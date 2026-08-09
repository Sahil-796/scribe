# Scoring rubric

For judging one writer output (a `runs/<run-id>/<item>.out.txt`) against the
corpus item's transcript slice it was run on. Read the corpus item's
`description` in `corpus/corpus.json` first — it says what the slice
actually contains, which is what "correct" is measured against.

Score each axis pass/fail, not a number — the point of this harness is
catching regressions between two prompt variants, not producing a leaderboard.
A variant that fails an axis on a corpus item it used to pass is the signal
that matters; the axes exist to say *which* failure, not to produce an
aggregate score.

## 1. Real problem and real fix

Does JOURNAL/CHANGELOG name the actual problem hit in the transcript and the
actual fix, not a paraphrase of the request? "Init works with real opencode;
found a reporting bug — claims four docs written, only two created" is a
pass. "Worked on init and fixed some issues" is a fail — it's true but
useless six months from now.

Fail this if the output invents a problem or fix that isn't in the
transcript at all (hallucination), or picks the wrong one when the slice
covers more than one.

## 2. No tool-call narration

Does the output avoid describing the mechanics of how work happened —
which files got opened, in what order, which command ran before which —
when that mechanic isn't itself the finding? A journal is about the problem
and the fix. "Read internal/worker/prompt.go, then internal/replay/prompt.go,
then ran go build, then edited three lines" is a fail even if every word is
true; a blow-by-blow of tool calls is not history, it's a transcript, and
scribe exists to be sharper than the transcript it reads.

Exception: a tool call is fair game to mention when its *result* is the
finding — "ran init against real opencode, immediately turned up a reporting
bug" names a fact worth knowing, not a mechanic.

## 3. Discussed vs. shipped

Does the output distinguish something that was actually implemented,
committed, or run and confirmed working from something that was only
proposed, discussed, or debated? A product conversation that settles on an
approach but writes no code is a DECISIONS.md candidate at most (something
was decided) — it is never a CHANGELOG entry (nothing changed). If the
corpus item is discussion-only (e.g. `product`), any output that claims a
feature "was added" or "was implemented" fails this axis outright, no matter
how well-written the prose is.

## 4. Dropped ideas get a reason, not a disappearance

When the transcript shows an idea, plan, or claim being raised and then
reversed or abandoned in the same slice, does the output say what was
dropped and why — not just record whichever thing survived? This is what
the `reversal` and `deadend` corpus items specifically probe. A pass looks
like DECISIONS.md's "dropped/superseded" convention: the old claim, marked
superseded, with the reason. A fail silently writes the final state as if
it had been the only state all along — technically not false, but it erases
a real correction that's worth being able to find later.

## 5. Terse

Is a CHANGELOG entry one line? Is a JOURNAL entry a few sentences that could
be read aloud without the listener's attention wandering — not a retelling
of the whole exchange in order? Padding a real finding with restated
transcript content, hedging, or "in this session we..." scene-setting fails
this axis even if axes 1-4 all pass. Terse is not "short at any cost" —
cutting the actual problem or the actual fix to hit a length target is a
different failure (see axis 1), not a pass here.

## Reading the results

For a single output: walk the five axes in order, note pass/fail with one
line of why. For comparing two variants on the same corpus item: put the
two `.out.txt` files side by side and note which axes flipped, not which
one "sounds better" — prose quality is not one of the five axes on purpose,
because it's the axis a cheap model is least reliably judged on and most
likely to reward regardless of what actually got recorded.
