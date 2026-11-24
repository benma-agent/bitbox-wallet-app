# SPDX-License-Identifier: Apache-2.0
# Run by windows-archive.ps1 with the source tree at B:/src.
set -euo pipefail

export PATH
PATH=$(B:/git/usr/bin/cygpath.exe -pu "${BITBOX_COMPILE_PATH:?missing verified tool paths}")
hash -r
check_command() {
    local resolved
    resolved=$(type -P "$1" || :)
    if [[ -z $resolved || ! $resolved -ef $2 ]]; then
        echo "Expected $1 at $2, Git Bash resolves it to '$resolved'." >&2
        exit 1
    fi
}
check_command git B:/git/cmd/git.exe
check_command bash B:/git/usr/bin/bash.exe
check_command node B:/node/node.exe
check_command npm B:/node/npm
check_command go B:/go/bin/go.exe
check_command gcc B:/mingw/bin/gcc.exe
check_command ld B:/mingw/bin/ld.exe
check_command qmake B:/qt/bin/qmake.exe
check_command rcc B:/qt/bin/rcc.exe
check_command windeployqt B:/qt/bin/windeployqt.exe
check_command cl "$BITBOX_VC_BIN/cl.exe"
check_command link "$BITBOX_VC_BIN/link.exe"
check_command rc "B:/kits/bin/$BITBOX_SDK_VERSION/x64/rc.exe"

# Check the fixed checkout from which the server build resolves gnark-crypto's
# importing packages before the assembler rewrites their shared source filenames.
fields=github.com/consensys/gnark-crypto/ecc/bls12-381
for package in "$fields/fp" "$fields/fr"; do
    dir=$(go -C B:/src list -mod=vendor -f '{{.Dir}}' "$package")
    dir=${dir%$'\r'}
    if [[ ${dir//\\//} != "B:/src/vendor/$package" ]]; then
        echo "Expected $package at B:/src/vendor/$package, Go resolved it to '$dir'." >&2
        exit 1
    fi
done

# Command-line make variables propagate to every recursive make, including the Go server.
exec "$BITBOX_WINDOWS_MAKE" "MAKE=$BITBOX_WINDOWS_MAKE" SHELL=B:/git/usr/bin/bash.exe qt-windows-release-payload
