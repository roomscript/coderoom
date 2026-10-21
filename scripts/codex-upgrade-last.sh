#!/bin/sh
# codex-upgrade-last: jump directly to the latest published stable Codex release.

set -e

target_version=$(npm view @openai/codex version)
test -n "$target_version"
if TARGET_CODEX_VERSION="$target_version" ./scripts/codex-upgrade-next.sh; then
    exit 0
else
    status=$?
    test "$status" -eq 10 || exit "$status"
fi
