#!/usr/bin/env bash
# pre-commit-spec-check.sh -- Block commits that include a PASSED validation
# report when the matching spec is still DRAFT.
#
# Install:
#   ln -sf ../../scripts/pre-commit-spec-check.sh .git/hooks/pre-commit
#
# The hook only inspects files staged for the current commit. It does NOT
# scan the entire repo -- only newly added or modified validation reports
# in the staging area trigger the check.

set -euo pipefail

SPEC_DIR="specs"
VALIDATION_DIR="validation-reports"

# Extract the spec number from a validation report.
# Checks the **Specs**: NNN line first, then falls back to filename pattern.
extract_spec_number() {
    local file="$1"
    local num

    # Try content match: **Specs**: NNN
    num=$(grep -oP '\*\*Specs?\*\*:\s*\K\d+' "$file" 2>/dev/null | head -1)
    if [[ -n "$num" ]]; then
        echo "$num"
        return
    fi

    # Fallback: filename contains spec-NNN or a known spec slug
    # Validation filenames don't embed spec numbers reliably, so this is
    # best-effort. If we can't determine the spec, skip the check for this file.
    echo ""
}

# Find the spec file for a given spec number (zero-padded 3-digit prefix).
find_spec_file() {
    local num="$1"
    local padded
    padded=$(printf "%03d" "$num")
    local match
    match=$(find "$SPEC_DIR" -maxdepth 1 -name "${padded}-*.md" -print -quit 2>/dev/null)
    echo "$match"
}

# Check if a spec file is still DRAFT.
is_draft() {
    local file="$1"
    grep -qP '^\*\*Status\*\*:\s*DRAFT' "$file" 2>/dev/null
}

# Check if a validation report is PASSED.
is_passed() {
    local file="$1"
    grep -qP '^\*\*Status\*\*:\s*PASSED' "$file" 2>/dev/null
}

errors=()

# Get staged files that are validation reports (added or modified).
while IFS= read -r file; do
    [[ -z "$file" ]] && continue
    [[ "$file" == "$VALIDATION_DIR"/* ]] || continue
    [[ "$file" == *.md ]] || continue

    # Only check PASSED reports.
    is_passed "$file" || continue

    spec_num=$(extract_spec_number "$file")
    [[ -n "$spec_num" ]] || continue

    spec_file=$(find_spec_file "$spec_num")
    [[ -n "$spec_file" ]] || continue

    if is_draft "$spec_file"; then
        padded=$(printf "%03d" "$spec_num")
        errors+=("  $file → spec $padded is still DRAFT ($spec_file)")
    fi
done < <(git diff --cached --name-only --diff-filter=AM)

if [[ ${#errors[@]} -gt 0 ]]; then
    echo "pre-commit: PASSED validation report(s) found with DRAFT spec(s):" >&2
    for err in "${errors[@]}"; do
        echo "$err" >&2
    done
    echo "" >&2
    echo "Mark the spec(s) as COMPLETE before committing, or use --no-verify to skip." >&2
    exit 1
fi
