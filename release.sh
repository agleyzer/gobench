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

go install -a -ldflags "-X main.version $VERSION"

cp $GOPATH/bin/gobench $GOPATH/bin/gobench.$VERSION

echo "$GOPATH/bin/gobench.$VERSION"
