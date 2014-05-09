#!/bin/bash

# Compiles gobench with version information

set -eu

function git_dirty() {
    [[ $(git status --porcelain 2> /dev/null) != "" ]] && echo "yes"
}

if [[ $(git_dirty) == "yes" ]]; then
   echo "Your git repo is dirty, yuck!" 1>&2
   exit 1
fi

VERSION=$(git log -1 --format='%h')

go clean

go build -ldflags "-X main.version $VERSION"

mv gobench gobench.$VERSION

echo "gobench.$VERSION"
