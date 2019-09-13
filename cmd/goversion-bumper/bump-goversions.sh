#!/bin/bash

set -ueo pipefail

GOVERSION_BUMPER="$(command -v goversion-bumper)"
if [ ! -x "$GOVERSION_BUMPER" ]; then
    echo "goversion-bumper not installed!"
    exit 1
fi

GO_VERSION=${GO_VERSION:-}
if [ -z "${GO_VERSION}" ]; then
    echo "GO_VERSION not set!"
    exit 1
fi

topdir="${PWD}"
github_user="${GITHUB_USER:-simonpasquier}"
branch="bump-golang-${GO_VERSION}"

rm -f open-prs.sh
touch open-prs.sh

# Iterate over all repositories. The GitHub API can return 100 items at most
# but it should be enough for us as there are less than 40 repositories
# currently.
for repo in $(curl --netrc --retry 5 --silent https://api.github.com/users/prometheus/repos?per_page=100 2>/dev/null | jq -r '.[] | select( .language == "Go" ) | .name'); do
    echo "Analyzing '${repo}'"

    cd "${topdir}/${repo}" || exit 1
    if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
        echo "✗✗✗ unclean repository!"
        continue
    fi
    if git rev-parse --verify --quiet "${branch}"; then
        echo "✗✗✗ ${branch} branch already exists!"
        continue
    fi

    git checkout master
    git pull
    "${GOVERSION_BUMPER}" -version "${GO_VERSION}"

    if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
        git checkout -b "${branch}"
        make unused
        git commit . -s -m "*: bump Go version to ${GO_VERSION}"
        if git push fork "${branch}"; then
          cat >> ../open-prs.sh <<EOF
            curl --netrc --show-error --silent \
                -X POST \
                -d "{\"title\":\"Bump Go version to ${GO_VERSION}\",\"base\":\"master\",\"head\":\"${github_user}:${branch}\",\"body\":\"\"}" \
                "https://api.github.com/repos/prometheus/${repo}/pulls"
EOF
        fi
    fi
done
