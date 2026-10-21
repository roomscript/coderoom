#!/bin/sh
# codex-upgrade-next: advance to the next or an explicitly targeted stable release,
# re-record transcripts, compare broad behavioral signals, and run replay plus
# live integration validation.
#
# Exit code 10 means CODEX_VERSION is already at the target or latest version.
#
# Tunables:
#   TARGET_CODEX_VERSION: jump directly to this published stable version
#                         instead of the next stable release
#   RECORD_RETRIES: number of record+replay attempts per scenario (default: 2)
#   ISOLATE_CODEX_HOME: 1 to use a fresh temporary CODEX_HOME per invocation
#                       seeded from auth/config, 0 to use the current home

set -e

TRANSCRIPT_ROOT=internal/agent/codex/testdata/transcripts
ALREADY_LATEST_EXIT=10
RECORD_RETRIES=${RECORD_RETRIES:-2}
ISOLATE_CODEX_HOME=${ISOLATE_CODEX_HOME:-1}
SOURCE_CODEX_HOME=${CODEX_HOME:-"$HOME/.codex"}

CURRENT=$(grep -v '^\#' CODEX_VERSION | tr -d '[:space:]')
TARGET_CODEX_VERSION=${TARGET_CODEX_VERSION:-}

if [ -z "$TARGET_CODEX_VERSION" ]; then
    LATEST=$(npm view @openai/codex version 2>/dev/null)
    if [ "$CURRENT" = "$LATEST" ]; then
        echo "codex is already at latest ($CURRENT)"
        exit "$ALREADY_LATEST_EXIT"
    fi
fi

VERSIONS=$(npm view @openai/codex versions --json 2>/dev/null)
NEXT=$(printf '%s\n' "$VERSIONS" | node -e '
const [current, target] = process.argv.slice(1);
const versions = JSON.parse(require("fs").readFileSync(0, "utf8"));
const stable = versions.filter(v => /^\d+\.\d+\.\d+$/.test(v));
const idx = stable.indexOf(current);
if (idx === -1) throw new Error("current version not found in stable list: " + current);
if (target) {
    const targetIdx = stable.indexOf(target);
    if (targetIdx === -1) throw new Error("target is not a published stable version: " + target);
    if (targetIdx < idx) throw new Error("target must not be older than current: " + current);
    console.log(target);
    process.exit(0);
}
if (idx + 1 >= stable.length) throw new Error("no stable version after current: " + current);
console.log(stable[idx + 1]);
' "$CURRENT" "$TARGET_CODEX_VERSION")

if [ "$NEXT" = "$CURRENT" ]; then
    echo "codex is already at target ($CURRENT)"
    exit "$ALREADY_LATEST_EXIT"
fi

echo "current: $CURRENT"
echo "next:    $NEXT"

seed_isolated_codex_home() {
    dst_home=$1

    mkdir -p "$dst_home"
    for file in auth.json config.toml version.json; do
        if [ -f "$SOURCE_CODEX_HOME/$file" ]; then
            cp "$SOURCE_CODEX_HOME/$file" "$dst_home/$file"
        fi
    done
}

run_with_codex_home() {
    if [ "$ISOLATE_CODEX_HOME" != "1" ]; then
        "$@"
        return
    fi

    temp_home=$(mktemp -d "${TMPDIR:-/tmp}/codex-upgrade-home-XXXXXX")
    seed_isolated_codex_home "$temp_home"

    set +e
    CODEX_HOME="$temp_home" "$@"
    status=$?
    set -e

    rm -rf "$temp_home"
    return "$status"
}

list_case_names() {
    version=$1
    src_dir="$TRANSCRIPT_ROOT/$version"

    for case_dir in "$src_dir"/*; do
        [ -d "$case_dir" ] || continue
        basename "$case_dir"
    done | sort
}

copy_transcript_input_case() {
    src_version=$1
    dst_version=$2
    case_name=$3
    src_dir="$TRANSCRIPT_ROOT/$src_version/$case_name"
    dst_dir="$TRANSCRIPT_ROOT/$dst_version/$case_name"

    rm -rf "$dst_dir"
    mkdir -p "$dst_dir"
    find "$src_dir" -maxdepth 1 -type f \
        \( -name 'prompt.md' -o -name 'conversation.md' -o -name 'conversation-*.md' \) \
        -exec cp {} "$dst_dir/" \;
}

init_version_dir() {
    version=$1
    dst_dir="$TRANSCRIPT_ROOT/$version"

    rm -rf "$dst_dir"
    mkdir -p "$dst_dir"
}

run_replay_suite() {
    go test ./internal/transcript ./cmd/codex-upgrade-compare ./cmd/codex-record ./cmd/codex-replay ./internal/agent/codex
}

run_replay_case() {
    version=$1
    case_name=$2
    go test ./internal/agent/codex -run "^TestReplayFixtures/${version}/${case_name}$"
}

run_live_suite() {
    version=$1
    CODEX_VERSION_OVERRIDE=$version run_with_codex_home \
        go test -tags integration ./internal/agent/codex -run 'TestClientLiveContextRetention|TestClientLiveTranscriptScenarios'
}

record_case_once() {
    version=$1
    case_name=$2
    previous_output="$TRANSCRIPT_ROOT/$CURRENT/$case_name/output.transcript"
    current_output="$TRANSCRIPT_ROOT/$version/$case_name/output.transcript"

    rm -f "$current_output"
    CODEX_VERSION_OVERRIDE=$version run_with_codex_home \
        go run ./cmd/codex-record "$version" "$case_name"
    go run ./cmd/codex-upgrade-compare "$previous_output" "$current_output"
}

record_case_with_retry() {
    version=$1
    case_name=$2
    attempt=1

    while [ "$attempt" -le "$RECORD_RETRIES" ]; do
        if record_case_once "$version" "$case_name" && run_replay_case "$version" "$case_name"; then
            return 0
        fi

        if [ "$attempt" -ge "$RECORD_RETRIES" ]; then
            return 1
        fi

        echo "  replay failed for $case_name; retrying with a fresh Codex home"
        attempt=$((attempt + 1))
        echo "-> attempt $attempt/$RECORD_RETRIES"
    done
}

echo "--- upgrading Codex transcripts to @$NEXT ---"
echo "phase: scaffold transcript inputs"
init_version_dir "$NEXT"

for case_name in $(list_case_names "$CURRENT"); do
    echo "phase: record + replay $case_name"
    copy_transcript_input_case "$CURRENT" "$NEXT" "$case_name"
    if ! record_case_with_retry "$NEXT" "$case_name"; then
        echo "failed to record a stable transcript for $case_name"
        exit 1
    fi
done

echo "phase: replay test suite"
run_replay_suite

echo "phase: live integration suite"
run_live_suite "$NEXT"

sed -i "s/^[0-9].*/$(echo "$NEXT")/" CODEX_VERSION
echo ""
echo "updated CODEX_VERSION to $NEXT"
echo "review transcript changes and commit CODEX_VERSION + updated transcript fixtures."
