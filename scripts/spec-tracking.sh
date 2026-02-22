#!/usr/bin/env bash
# spec-tracking.sh -- sync spec lifecycle to GitHub Issues.
#
# Usage:
#   spec-tracking.sh push                    Process specs/validations changed in the current push
#   spec-tracking.sh backfill [--dry-run]    Create issues for all existing specs and close resolved ones

set -euo pipefail

# --- Constants ---------------------------------------------------------------

SPEC_DIR="specs"
VALIDATION_DIR="validation-reports"
SPEC_LABEL="spec-tracking"
BACKFILL_LABEL="backfill"
SPEC_LABEL_COLOR="0E8A16"
BACKFILL_LABEL_COLOR="D4C5F9"
REPO_URL="${GITHUB_SERVER_URL:-https://github.com}/${GITHUB_REPOSITORY:-ushineko/face-puncher-supreme}"

# --- CLI Parsing -------------------------------------------------------------

MODE="${1:?Usage: spec-tracking.sh <push|backfill> [--dry-run]}"
shift
DRY_RUN="false"
for arg in "$@"; do
    case "$arg" in
        --dry-run|--dry-run=true)  DRY_RUN="true" ;;
        --dry-run=false)           DRY_RUN="false" ;;
        *) echo "Unknown argument: $arg" >&2; exit 1 ;;
    esac
done

# --- Helpers -----------------------------------------------------------------

log() { echo ":: $*"; }
dry() { echo "[dry-run] would: $*"; }

# Ensure a label exists, creating it if missing.
ensure_label() {
    local name="$1" color="$2" description="$3"
    if ! gh label list --json name -q ".[].name" | grep -qx "$name"; then
        if [[ "$DRY_RUN" == "true" ]]; then
            dry "create label '$name' ($color)"
        else
            gh label create "$name" --color "$color" --description "$description" || true
        fi
    fi
}

ensure_labels() {
    ensure_label "$SPEC_LABEL" "$SPEC_LABEL_COLOR" "Auto-tracked spec lifecycle issue"
    ensure_label "$BACKFILL_LABEL" "$BACKFILL_LABEL_COLOR" "Retroactively created spec issue"
}

# --- Spec Parsing ------------------------------------------------------------

# Parse a spec file and print key=value lines to stdout.
# Caller evals the output: eval "$(parse_spec specs/001-foo.md)"
parse_spec() {
    local file="$1"
    local filename
    filename=$(basename "$file")

    local num="" title="" status="" created="" deps=""

    # Extract from H1 heading: # Spec NNN: Title
    local line1
    line1=$(head -1 "$file")
    if [[ "$line1" =~ ^#\ Spec\ ([0-9]{3}):\ (.+)$ ]]; then
        num="${BASH_REMATCH[1]}"
        title="${BASH_REMATCH[2]}"
    fi

    # Extract bold front matter fields from the first 10 lines
    while IFS= read -r line; do
        if [[ "$line" =~ ^\*\*Status\*\*:\ (.+)$ ]]; then
            status="${BASH_REMATCH[1]}"
        elif [[ "$line" =~ ^\*\*Created\*\*:\ ([0-9]{4}-[0-9]{2}-[0-9]{2})$ ]]; then
            created="${BASH_REMATCH[1]}"
        elif [[ "$line" =~ ^\*\*Depends\ on\*\*:\ (.+)$ ]]; then
            deps="${BASH_REMATCH[1]}"
        fi
    done < <(head -10 "$file")

    # If no Created date in front matter, try git log for first commit
    if [[ -z "$created" ]]; then
        created=$(git log --diff-filter=A --format='%as' -- "$file" 2>/dev/null | tail -1) || true
    fi

    printf 'SPEC_NUM=%q\n' "$num"
    printf 'SPEC_TITLE=%q\n' "$title"
    printf 'SPEC_STATUS=%q\n' "$status"
    printf 'SPEC_CREATED=%q\n' "${created:-not recorded}"
    printf 'SPEC_DEPS=%q\n' "${deps:-none}"
    printf 'SPEC_FILE=%q\n' "$file"
}

# --- Validation Report Parsing -----------------------------------------------

# Parse a validation report and print key=value lines.
parse_validation() {
    local file="$1"
    local filename
    filename=$(basename "$file")

    local date="" status="" spec_refs=""

    # Extract date and status from front matter (first 10 lines)
    while IFS= read -r line; do
        if [[ "$line" =~ ^\*\*Date\*\*:\ ([0-9]{4}-[0-9]{2}-[0-9]{2})\ ([0-9]{2}:[0-9]{2})$ ]]; then
            date="${BASH_REMATCH[1]} ${BASH_REMATCH[2]}"
        elif [[ "$line" =~ ^\*\*Date\*\*:\ ([0-9]{4}-[0-9]{2}-[0-9]{2})$ ]]; then
            date="${BASH_REMATCH[1]}"
        elif [[ "$line" =~ ^\*\*Status\*\*:\ (PASSED|FAILED)$ ]]; then
            status="${BASH_REMATCH[1]}"
        fi
    done < <(head -10 "$file")

    # Extract explicit spec references from front matter (first 10 lines)
    # Matches: **Spec**: 010  or  **Specs**: 010, 011  or  **Specs**: 010 (desc), 011 (desc)
    local refs_line
    refs_line=$(head -10 "$file" | grep -iE '^\*\*Specs?\*\*:' || true)
    if [[ -n "$refs_line" ]]; then
        spec_refs=$(echo "$refs_line" | grep -oE '[0-9]{3}' | tr '\n' ' ')
    fi

    printf 'VAL_DATE=%q\n' "${date:-unknown}"
    printf 'VAL_STATUS=%q\n' "${status:-unknown}"
    printf 'VAL_SPEC_REFS=%q\n' "$spec_refs"
    printf 'VAL_FILE=%q\n' "$file"
}

# --- Slug Extraction ---------------------------------------------------------

# Strip date prefix from validation filename to get topic slug.
# "2026-02-16-0300-domain-blocklist.md" → "domain-blocklist"
slug_from_validation() {
    local filename
    filename=$(basename "$1" .md)
    echo "$filename" | sed -E 's/^[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{4}-//'
}

# Strip number prefix from spec filename to get topic slug.
# "002-domain-blocklist.md" → "domain-blocklist"
slug_from_spec() {
    local filename
    filename=$(basename "$1" .md)
    echo "$filename" | sed -E 's/^[0-9]{3}-//'
}

# --- Topic Matching ----------------------------------------------------------

# Count shared hyphen-delimited tokens between two slugs.
common_token_count() {
    local slug1="$1" slug2="$2"
    local count=0
    local -a tokens1 tokens2
    IFS='-' read -ra tokens1 <<< "$slug1"
    IFS='-' read -ra tokens2 <<< "$slug2"
    for t1 in "${tokens1[@]}"; do
        for t2 in "${tokens2[@]}"; do
            if [[ "$t1" == "$t2" ]]; then
                ((count++))
                break
            fi
        done
    done
    echo "$count"
}

# Match a validation report to spec number(s). Prints space-separated spec numbers.
# Returns empty string if no match (caller handles fallback).
match_validation_to_specs() {
    local val_file="$1"
    local val_filename
    val_filename=$(basename "$val_file")

    # Priority 1: Explicit spec number in validation filename
    if [[ "$val_filename" =~ spec-([0-9]{3}) ]]; then
        echo "${BASH_REMATCH[1]}"
        return
    fi

    # Priority 2: Explicit spec reference in validation content
    eval "$(parse_validation "$val_file")"
    if [[ -n "${VAL_SPEC_REFS:-}" && "${VAL_SPEC_REFS:-}" != " " ]]; then
        echo "$VAL_SPEC_REFS"
        return
    fi

    # Priority 3: Topic keyword matching
    local val_slug
    val_slug=$(slug_from_validation "$val_file")

    local best_spec="" best_count=0 tied=0
    for spec_file in "$SPEC_DIR"/*.md; do
        [[ -f "$spec_file" ]] || continue
        local spec_slug
        spec_slug=$(slug_from_spec "$spec_file")
        local count
        count=$(common_token_count "$val_slug" "$spec_slug")
        if (( count > best_count )); then
            best_count=$count
            best_spec=$(basename "$spec_file" .md | grep -oE '^[0-9]{3}')
            tied=0
        elif (( count == best_count && count > 0 )); then
            ((tied++))
        fi
    done

    # Require >= 2 tokens, or >= 1 if unambiguous (no tie)
    if (( best_count >= 2 )); then
        echo "$best_spec"
        return
    elif (( best_count == 1 && tied == 0 )); then
        echo "$best_spec"
        return
    fi

    # Priority 4: No match — return empty, caller handles fallback
    echo ""
}

# --- Issue Operations --------------------------------------------------------

# Find an existing issue by spec number. Returns issue number or empty.
find_spec_issue() {
    local spec_num="$1"
    local title_prefix="[spec-${spec_num}]"
    gh issue list --label "$SPEC_LABEL" --state all --json number,title -q \
        ".[] | select(.title | startswith(\"$title_prefix\")) | .number" 2>/dev/null | head -1
}

# Build the issue body for a spec.
build_issue_body() {
    local spec_file="$1" is_backfill="$2"

    eval "$(parse_spec "$spec_file")"
    local file_url="${REPO_URL}/blob/main/${spec_file}"

    local body=""
    body+="## Spec: ${SPEC_TITLE}"$'\n\n'
    body+="**Spec file**: [\`${spec_file}\`](${file_url})"$'\n'
    body+="**Status**: ${SPEC_STATUS}"$'\n'
    body+="**Created**: ${SPEC_CREATED}"$'\n'
    body+="**Depends on**: ${SPEC_DEPS}"$'\n\n'
    body+="---"$'\n\n'
    body+="> This issue tracks the lifecycle of a development spec."$'\n'
    body+="> It was created automatically by the spec-tracking workflow."$'\n'
    body+="> See the linked spec file for full requirements and acceptance criteria."

    if [[ "$is_backfill" == "true" ]]; then
        body+=$'\n\n'
        body+="> **Backfill notice**: This issue was created retroactively by the spec-tracking"$'\n'
        body+="> workflow. The spec was originally authored on ${SPEC_CREATED}. The issue creation"$'\n'
        body+="> date does not reflect when the work was done."
    fi

    echo "$body"
}

# Create a GitHub issue for a spec. Returns the new issue number.
create_spec_issue() {
    local spec_file="$1" is_backfill="$2"

    eval "$(parse_spec "$spec_file")"
    local title="[spec-${SPEC_NUM}] ${SPEC_TITLE}"
    local body
    body=$(build_issue_body "$spec_file" "$is_backfill")

    local labels="$SPEC_LABEL"
    if [[ "$is_backfill" == "true" ]]; then
        labels="${SPEC_LABEL},${BACKFILL_LABEL}"
    fi

    if [[ "$DRY_RUN" == "true" ]]; then
        dry "create issue '${title}' (labels: ${labels})"
        return
    fi

    local issue_url issue_num
    issue_url=$(gh issue create --title "$title" --body "$body" --label "$labels")
    issue_num="${issue_url##*/}"
    log "Created issue #${issue_num}: ${title}"
    echo "$issue_num"
}

# Update an existing issue body.
update_spec_issue() {
    local issue_num="$1" spec_file="$2" is_backfill="$3"

    local body
    body=$(build_issue_body "$spec_file" "$is_backfill")

    if [[ "$DRY_RUN" == "true" ]]; then
        dry "update issue #${issue_num} body"
        return
    fi

    gh issue edit "$issue_num" --body "$body"
    log "Updated issue #${issue_num}"
}

# Close a spec issue with a validation report comment.
close_spec_issue() {
    local issue_num="$1" val_file="$2"

    eval "$(parse_validation "$val_file")"
    local file_url="${REPO_URL}/blob/main/${val_file}"

    local comment=""
    comment+="Validated and closing."$'\n\n'
    comment+="**Validation report**: [\`${val_file}\`](${file_url})"$'\n'
    comment+="**Validation date**: ${VAL_DATE}"$'\n'
    comment+="**Validation status**: ${VAL_STATUS}"$'\n\n'
    comment+="All acceptance criteria met per the linked validation report."

    if [[ "$DRY_RUN" == "true" ]]; then
        dry "close issue #${issue_num} with validation comment (${val_file})"
        return
    fi

    gh issue close "$issue_num" --comment "$comment"
    log "Closed issue #${issue_num} with validation: ${val_file}"
}

# Close a spec issue based on its COMPLETE status (no validation report).
close_spec_issue_by_status() {
    local issue_num="$1" spec_file="$2"

    eval "$(parse_spec "$spec_file")"

    local comment=""
    comment+="Closing based on spec status."$'\n\n'
    comment+="**Spec status**: ${SPEC_STATUS}"$'\n\n'
    comment+="No matching validation report found, but the spec is marked COMPLETE."

    if [[ "$DRY_RUN" == "true" ]]; then
        dry "close issue #${issue_num} by COMPLETE status (no validation)"
        return
    fi

    gh issue close "$issue_num" --comment "$comment"
    log "Closed issue #${issue_num} by COMPLETE status"
}

# Add a supplementary validation comment without closing.
add_validation_comment() {
    local issue_num="$1" val_file="$2"

    eval "$(parse_validation "$val_file")"
    local file_url="${REPO_URL}/blob/main/${val_file}"

    local comment=""
    comment+="Supplementary validation report (not directly linked to this spec)."$'\n\n'
    comment+="**Validation report**: [\`${val_file}\`](${file_url})"$'\n'
    comment+="**Validation date**: ${VAL_DATE}"$'\n'
    comment+="**Validation status**: ${VAL_STATUS}"

    if [[ "$DRY_RUN" == "true" ]]; then
        dry "add supplementary comment to issue #${issue_num} (${val_file})"
        return
    fi

    gh issue comment "$issue_num" --body "$comment"
    log "Added supplementary comment to issue #${issue_num}: ${val_file}"
}

# --- Push Mode ---------------------------------------------------------------

do_push() {
    local before="${BEFORE_SHA:-}"
    local after="${AFTER_SHA:-HEAD}"

    # Determine changed files
    local changed_files
    if [[ -z "$before" || "$before" == "0000000000000000000000000000000000000000" ]]; then
        # First push or force push — compare against empty tree
        changed_files=$(git diff --name-only "4b825dc642cb6eb9a060e54bf899d15f4c1d7c28..$after" -- "$SPEC_DIR/" "$VALIDATION_DIR/" 2>/dev/null || true)
    else
        changed_files=$(git diff --name-only "$before..$after" -- "$SPEC_DIR/" "$VALIDATION_DIR/" 2>/dev/null || true)
    fi

    if [[ -z "$changed_files" ]]; then
        log "No spec or validation report changes detected"
        return
    fi

    log "Changed files:"
    while IFS= read -r f; do echo "  $f"; done <<< "$changed_files"

    # Process changed specs
    local changed_specs
    changed_specs=$(echo "$changed_files" | grep "^${SPEC_DIR}/" || true)
    for spec_file in $changed_specs; do
        [[ -f "$spec_file" ]] || continue
        eval "$(parse_spec "$spec_file")"
        [[ -n "$SPEC_NUM" ]] || continue

        local existing
        existing=$(find_spec_issue "$SPEC_NUM")
        if [[ -z "$existing" ]]; then
            create_spec_issue "$spec_file" "false"
        else
            update_spec_issue "$existing" "$spec_file" "false"
        fi
    done

    # Process changed validation reports
    local changed_vals
    changed_vals=$(echo "$changed_files" | grep "^${VALIDATION_DIR}/" || true)
    for val_file in $changed_vals; do
        [[ -f "$val_file" ]] || continue
        eval "$(parse_validation "$val_file")"

        local matched_specs
        matched_specs=$(match_validation_to_specs "$val_file")
        if [[ -z "$matched_specs" ]]; then
            log "No spec match for ${val_file} — skipping"
            continue
        fi

        for spec_num in $matched_specs; do
            spec_num=$(echo "$spec_num" | tr -d '[:space:]')
            [[ -n "$spec_num" ]] || continue

            local issue_num
            issue_num=$(find_spec_issue "$spec_num")
            if [[ -z "$issue_num" ]]; then
                log "No issue found for spec ${spec_num} — skipping validation linkage"
                continue
            fi

            # Check if issue is still open
            local state
            state=$(gh issue view "$issue_num" --json state -q '.state' 2>/dev/null || echo "UNKNOWN")
            if [[ "$state" == "OPEN" && "$VAL_STATUS" == "PASSED" ]]; then
                close_spec_issue "$issue_num" "$val_file"
            elif [[ "$state" == "OPEN" ]]; then
                add_validation_comment "$issue_num" "$val_file"
            else
                log "Issue #${issue_num} already closed — skipping"
            fi
        done
    done
}

# --- Backfill Mode -----------------------------------------------------------

do_backfill() {
    log "Starting backfill (dry_run=${DRY_RUN})"

    # Phase 1: Create issues for all specs
    log "Phase 1: Creating issues for all specs..."
    declare -A spec_issue_map  # spec_num -> issue_num

    for spec_file in "$SPEC_DIR"/*.md; do
        [[ -f "$spec_file" ]] || continue
        eval "$(parse_spec "$spec_file")"
        [[ -n "$SPEC_NUM" ]] || continue

        local existing
        existing=$(find_spec_issue "$SPEC_NUM")
        if [[ -n "$existing" ]]; then
            log "Issue already exists for spec ${SPEC_NUM}: #${existing}"
            spec_issue_map[$SPEC_NUM]="$existing"
        else
            local new_issue
            new_issue=$(create_spec_issue "$spec_file" "true")
            if [[ -n "$new_issue" ]]; then
                spec_issue_map[$SPEC_NUM]="$new_issue"
            fi
        fi
    done

    # Phase 2: Match validation reports to specs and close/comment
    log "Phase 2: Processing validation reports..."
    declare -A spec_has_validation  # spec_num -> "true" if a PASSED validation matched

    for val_file in "$VALIDATION_DIR"/*.md; do
        [[ -f "$val_file" ]] || continue
        eval "$(parse_validation "$val_file")"

        local matched_specs
        matched_specs=$(match_validation_to_specs "$val_file")

        if [[ -z "$matched_specs" ]]; then
            # Fallback: attach to spec 001
            log "No match for $(basename "$val_file") — attaching to spec 001 as supplementary"
            local fallback_issue="${spec_issue_map[001]:-}"
            if [[ -n "$fallback_issue" ]]; then
                add_validation_comment "$fallback_issue" "$val_file"
            fi
            continue
        fi

        for spec_num in $matched_specs; do
            spec_num=$(echo "$spec_num" | tr -d '[:space:]')
            [[ -n "$spec_num" ]] || continue

            local issue_num="${spec_issue_map[$spec_num]:-}"
            if [[ -z "$issue_num" ]]; then
                log "No issue for spec ${spec_num} — skipping"
                continue
            fi

            if [[ "$VAL_STATUS" == "PASSED" ]]; then
                spec_has_validation[$spec_num]="true"

                # Check if issue is still open before closing
                if [[ "$DRY_RUN" == "true" ]]; then
                    close_spec_issue "$issue_num" "$val_file"
                else
                    local state
                    state=$(gh issue view "$issue_num" --json state -q '.state' 2>/dev/null || echo "UNKNOWN")
                    if [[ "$state" == "OPEN" ]]; then
                        close_spec_issue "$issue_num" "$val_file"
                    else
                        log "Issue #${issue_num} (spec ${spec_num}) already closed — adding comment only"
                        add_validation_comment "$issue_num" "$val_file"
                    fi
                fi
            else
                add_validation_comment "$issue_num" "$val_file"
            fi
        done
    done

    # Phase 3: Close COMPLETE specs that have no validation report
    log "Phase 3: Closing COMPLETE specs without validation..."
    for spec_file in "$SPEC_DIR"/*.md; do
        [[ -f "$spec_file" ]] || continue
        eval "$(parse_spec "$spec_file")"
        [[ -n "$SPEC_NUM" ]] || continue

        if [[ "$SPEC_STATUS" == "COMPLETE" && "${spec_has_validation[$SPEC_NUM]:-}" != "true" ]]; then
            local issue_num="${spec_issue_map[$SPEC_NUM]:-}"
            if [[ -n "$issue_num" ]]; then
                if [[ "$DRY_RUN" == "true" ]]; then
                    close_spec_issue_by_status "$issue_num" "$spec_file"
                else
                    local state
                    state=$(gh issue view "$issue_num" --json state -q '.state' 2>/dev/null || echo "UNKNOWN")
                    if [[ "$state" == "OPEN" ]]; then
                        close_spec_issue_by_status "$issue_num" "$spec_file"
                    fi
                fi
            fi
        fi
    done

    log "Backfill complete"
}

# --- Main --------------------------------------------------------------------

ensure_labels

case "$MODE" in
    push)     do_push ;;
    backfill) do_backfill ;;
    *)        echo "Unknown mode: $MODE" >&2; exit 1 ;;
esac
