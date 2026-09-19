#!/usr/bin/env bash
# spec-tracking-parse-test.sh -- pin what spec-tracking.sh can parse.
#
# The parsers read files written by hand across several repositories, and the
# repositories grew different conventions: this one puts a spec's status in
# bold front matter, clockwork-orange and fynedesygn put it in a heading; this
# one names a validation report spec-024, they name it spec014; and a verdict
# here is sometimes qualified ("PASSED (AC9 remains for the author)").
#
# A parser that silently fails to read one of those does not report an error --
# it creates no issue, or closes none, which looks exactly like having nothing
# to do. Hence fixtures rather than trust.

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
SCRIPT="scripts/spec-tracking.sh"
fixtures=$(mktemp -d)
trap 'rm -rf "$fixtures"' EXIT

fail=0
check() {
    local what="$1" want="$2" got="$3"
    if [[ "$want" != "$got" ]]; then
        echo "FAIL  $what: want '$want', got '$got'" >&2
        fail=1
    else
        echo "ok    $what"
    fi
}

# parse_spec and parse_validation are sourced out of the script rather than
# copied, so this tests the shipped code and not a transcription of it.
eval "$(sed -n '/^parse_spec()/,/^}/p;/^parse_validation()/,/^}/p' "$SCRIPT")"

spec_status() {
    local body="$1"
    printf '%s' "$body" > "$fixtures/spec.md"
    eval "$(parse_spec "$fixtures/spec.md")"
    printf '%s|%s' "$SPEC_NUM" "$SPEC_STATUS"
}

check "bold front matter (face-puncher-supreme)" "024|COMPLETE" \
    "$(spec_status '# Spec 024: Transparent CONNECT
**Status**: COMPLETE
**Created**: 2026-05-29
')"

check "heading status (clockwork-orange, fynedesygn)" "014|COMPLETE" \
    "$(spec_status '# Spec 014: Affixed controls

> **Note**: no ticket.

## Status: COMPLETE

- **Priority**: Medium
')"

check "a status with a qualifier after it" "010|IN_PROGRESS (4 criteria host-gated)" \
    "$(spec_status '# Spec 010: Go rewrite

## Status: IN_PROGRESS (4 criteria host-gated)
')"

val() {
    local name="$1" body="$2"
    printf '%s' "$body" > "$fixtures/$name"
    eval "$(parse_validation "$fixtures/$name")"
    local num=""
    [[ "$name" =~ spec-?([0-9]{3}) ]] && num="${BASH_REMATCH[1]}"
    printf '%s|%s|%s' "$num" "$VAL_DATE" "$VAL_STATUS"
}

check "hyphenated filename, bare verdict" "024|2026-05-29 18:32|PASSED" \
    "$(val 'spec-024-x.md' '# Validation
**Date**: 2026-05-29 18:32
**Status**: PASSED
')"

check "unhyphenated filename (clockwork-orange)" "014|2026-09-18|PASSED" \
    "$(val '2026-09-18-2043-spec014-affixed.md' '# Validation
**Date**: 2026-09-18
**Status**: PASSED
')"

check "a verdict with a qualifier after it" "015|2026-09-18|PASSED" \
    "$(val 'spec015-x.md' '# Validation
**Date**: 2026-09-18
**Status**: PASSED (AC9, the manual desktop run, remains for the author)
')"

check "a failure is still a failure" "009|2026-09-18|FAILED" \
    "$(val 'spec009-x.md' '# Validation
**Date**: 2026-09-18
**Status**: FAILED
')"

# A word that merely begins with the verdict is not the verdict.
check "PASSEDISH is not PASSED" "008|2026-09-18|unknown" \
    "$(val 'spec008-x.md' '# Validation
**Date**: 2026-09-18
**Status**: PASSEDISH
')"

check "prose date (fynedesygn), no verdict line" "005|2026-09-18|unknown" \
    "$(val 'spec005-x.md' '# Validation report: spec 005

Date: 2026-09-18. Milestone: the reference programs.
')"

exit "$fail"
