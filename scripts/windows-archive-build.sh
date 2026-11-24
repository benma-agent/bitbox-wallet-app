# SPDX-License-Identifier: Apache-2.0
# Run by windows-archive.ps1 from the checkout being built.
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

# Command-line make variables propagate to every recursive make, including the Go server.
exec "$BITBOX_WINDOWS_MAKE" "MAKE=$BITBOX_WINDOWS_MAKE" SHELL=B:/git/usr/bin/bash.exe qt-windows-release-payload
