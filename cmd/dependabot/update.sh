#!/bin/bash

set -e

TMP_DIR="$(mktemp)"

if [ -z "$REPOSITORY_URL" ]; then
    echo "REPOSITORY_URL not set"
    exit 1
fi

if [ -z "$PULL_REQUEST_BRANCH" ]; then
    echo "PULL_REQUEST_BRANCH not set"
    exit 1
fi

cd "$TMP_DIR"
git clone --depth 1 --branch "$GITHUB_PULL_REQUEST_BRANCH"  git@github.com:"$GITHUB_OWNER"/"$GITHUB_REPOSITORY"

if make unused; then
    echo "Everything is fine, nothing to update..."
    exit 0
fi
git add go.mod go.sum vendor/
git commit -m "Update vendor/"
git push --quiet origin "$PULL_REQUEST_BRANCH"
