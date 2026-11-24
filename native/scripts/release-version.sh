#!/usr/bin/env bash
set -euo pipefail

if [[ $(git rev-parse --is-shallow-repository) == true ]]; then
    printf 'Version calculation requires full Git history and tags.\n' >&2
    exit 1
fi

# Only manually managed integer tags are version bases. Generated releases
# (vN.F.O) and historical build tags must not reset the counters.
tags=$(git tag --merged HEAD --sort=-version:refname --sort=-creatordate)
major=0
range=HEAD
while IFS= read -r tag; do
    if [[ $tag =~ ^v(0|[1-9][0-9]*)$ ]]; then
        major=${BASH_REMATCH[1]}
        range="$tag..HEAD"
        break
    fi
done <<< "$tags"

subjects=$(git log --format='x%s' "$range" --)
feature_pattern='^xfeat(\([^)]+\))?!?:'
feat=0
other=0
while IFS= read -r subject; do
    [[ -n $subject ]] || continue
    if [[ $subject =~ $feature_pattern ]]; then
        feat=$((feat + 1))
    else
        other=$((other + 1))
    fi
done <<< "$subjects"
printf '%s.%s.%s\n' "$major" "$feat" "$other"
