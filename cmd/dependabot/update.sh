#!/bin/bash

set -e

if [ -z "$GITHUB_OWNER" ]; then
    echo "GITHUB_OWNER not set"
    exit 1
fi

if [ -z "$GITHUB_REPOSITORY" ]; then
    echo "GITHUB_REPOSITORY not set"
    exit 1
fi

if [ -z "$GITHUB_BRANCH" ]; then
    echo "GITHUB_BRANCH not set"
    exit 1
fi

TMP_DIR="$(mktemp -d)"
trap 'rm -rf $TMP_DIR' SIGINT SIGTERM EXIT
git clone --depth 1 --branch "$GITHUB_BRANCH"  git@github.com:"$GITHUB_OWNER"/"$GITHUB_REPOSITORY" "$TMP_DIR"
cd "$TMP_DIR"

CHANGES=
git log -1
# TODO: deal with elm updates too...
set +e
make unused
set -e
if ! git diff --quiet; then
    git add vendor/
    git commit . --signoff -m "Update vendor/"
fi

set +e
make proto
set -e
if ! git diff --quiet; then
    git commit . --signoff -m "Update protobuf code"
fi

git push --quiet origin "$GITHUB_BRANCH"
