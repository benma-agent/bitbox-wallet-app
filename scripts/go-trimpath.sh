#!/bin/bash
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

# Go and gomobile can target a different OS or architecture from the build host.
# Go runs -toolexec tools on the host, including when gomobile builds generated modules.
tools=$(mktemp -d)
trap 'rm -rf "$tools"' EXIT
host_os=$(go env GOHOSTOS)
host_arch=$(go env GOHOSTARCH)
GOOS="${host_os%$'\r'}" GOARCH="${host_arch%$'\r'}" CGO_ENABLED=0 GOWORK=off GOFLAGS=-trimpath \
    go -C "$(dirname "$0")/trim-go-asm" build -buildvcs=false -mod=vendor -o "$tools/" .

flags=$(go env GOFLAGS)
# gomobile starts go build in its own module. Pass the tool by name so its temporary
# installation directory can contain spaces and each child process can find it.
export PATH="$tools:$PATH"
export GOFLAGS="${flags%$'\r'} -toolexec=trim-go-asm"
"$@"
