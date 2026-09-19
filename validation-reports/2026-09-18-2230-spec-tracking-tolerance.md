# Validation Report: spec-tracking parser tolerance

**Date**: 2026-09-18 22:30
**Status**: PASSED

## Scope

No spec: a fix to CI tooling, made while adopting `spec-tracking.sh` in the
other two ushineko Go repositories.

## Summary

The parsers read files written by hand across repositories that grew different
conventions, and four of those differences made the script read nothing without
reporting anything:

| | this repo | clockwork-orange / fynedesygn |
|---|---|---|
| spec status | `**Status**: COMPLETE` | `## Status: COMPLETE` |
| report filename | `spec-024` | `spec014` |
| report verdict | `**Status**: PASSED` | `**Status**: PASSED (AC9 …)` |
| report date | `**Date**: …` | `Date: …` (prose) |

A parser that cannot read a file does not fail here: it creates no issue, or
closes none, which is indistinguishable from having nothing to do. The failure
mode is silence, which is why this went to fixtures rather than inspection.

Changes, all widening:

- A spec's status is read from `**Status**:` or from a `## Status:` heading,
  and from the first 20 lines rather than the first 10, because the heading
  form sits below a note block.
- A validation filename matches `spec-?NNN`, so `spec-024` and `spec014` are
  the same reference.
- A verdict may carry a qualifier — "PASSED (AC9, the manual desktop run,
  remains for the author)" — and is still the verdict. Anchored on a
  non-letter, so `PASSEDISH` is not read as a pass.
- A prose `Date: YYYY-MM-DD` is read as a date.

## Tests

`scripts/spec-tracking-parse-test.sh`, nine fixtures covering both spellings of
each convention. It sources `parse_spec` and `parse_validation` out of the
shipped script with `sed` rather than copying them, so it tests the code that
runs and not a transcription of it.

Checked against the bug: with the previous parser, **four of the nine fail**.

`bash -n` clean.

## Security Review

A shell script that runs in Actions with `issues: write`. The change is four
regular expressions, all narrowing what is accepted into a capture group; no
new command is run, no new input is read, and nothing reaches a shell. The
`PASSEDISH` fixture exists because widening a verdict match is the one place
where a looser regex could assert something false.

## Release Safety

Rollback: revert the commit. The workflow is idempotent — it creates issues
that do not exist and closes ones that are open — so a revert stops new
tracking without leaving anything half-done.

## Overall

**PASSED.**
