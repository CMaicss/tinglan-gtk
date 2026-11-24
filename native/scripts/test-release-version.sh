#!/usr/bin/env bash
set -euo pipefail
script=$(cd -- "$(dirname -- "$0")" && pwd)/release-version.sh
repo=$(mktemp -d "${TMPDIR:-/tmp}/tinglan-version-test.XXXXXX")
trap 'rm -rf -- "$repo"' EXIT
cd "$repo"
git init -q -b main
git config user.name Test
git config user.email test@example.invalid
commit() { git commit -q --allow-empty -m "$1"; }
expect() {
    local actual
    actual=$(bash "$script")
    [[ $actual == "$1" ]] || { printf 'want %s, got %s\n' "$1" "$actual" >&2; exit 1; }
}
commit 'fix: initial'
commit 'feat: initial feature'
git tag v0.0.3-build.7
expect 0.1.1
git tag -a v2 -m 'version base'
expect 2.0.0
commit 'feat(ui): next'
commit 'feat!: breaking'
commit 'feature: not Conventional Commits'
commit 'docs: update'
git tag v2.2.2
expect 2.2.2
git switch -qc unrelated HEAD~2
commit 'fix: branch only'
# A later tag on an unmerged branch must not set the base on main.
GIT_COMMITTER_DATE=2030-01-01T00:00:00Z git tag -a v9 -m 'branch base'
git switch -q main
expect 2.2.2
git merge -q --no-ff unrelated -m 'Merge branch'
expect 9.0.3
commit 'feat(core)!: change'
expect 9.1.3
printf 'release-version tests passed\n'
