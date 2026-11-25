# SPDX-License-Identifier: Apache-2.0
set -euo pipefail

# Git Bash converts the verified Windows PATH at this boundary. Recursive Make uses
# the same pinned GNU Make and Bash; developer-shell settings cannot select other tools.
export PATH
PATH=$(B:/git/usr/bin/cygpath.exe -pu "${BITBOX_COMPILE_PATH:?Use make qt-windows}")
cd "$(dirname "$0")"
build_dir=${1:?missing build directory}
windows="${build_dir%/}/windows"
build_make=("$BITBOX_WINDOWS_MAKE" "MAKE=$BITBOX_WINDOWS_MAKE" SHELL=B:/git/usr/bin/bash.exe)
"${build_make[@]}" -C ../.. buildweb
# windeployqt can leave files from earlier deployments in the output directory.
rm -rf "$windows"
mkdir -p "$windows"
bash ./genassets.sh "$windows/assets.rcc"
"${build_make[@]}" -C server/ windows-legacy SERVER_OUT="$build_dir"
# Disable Git Bash path rewriting for cmd's /d and /c switches.
# /d skips AutoRun hooks; /c exits after the batch file.
# Clear GNU Make's recursive flags before entering NMake.
MSYS2_ARG_CONV_EXCL='*' env -u MAKE -u MAKEFLAGS cmd.exe /d /c compile_windows.bat "$build_dir"
cp "$build_dir/libserver.dll" "$windows/"
cp ../../vendor/github.com/breez/breez-sdk-spark-go/breez_sdk_spark/lib/windows-amd64/breez_sdk_spark_bindings.dll "$windows/"
# The Qt kit supplies its own Microsoft D3D compiler. Host Windows versions can differ.
cp "$BITBOX_QT_BIN/d3dcompiler_47.dll" "$windows/D3Dcompiler_47.dll"
# The release includes Qt Positioning's NMEA plug-in. Deploy that file directly
# because scanning it also pulls in Qt Serial Port on kits with that optional module.
exclude_plugins="qtposition_nmea${BITBOX_QT_EXCLUDE_PLUGINS:+,$BITBOX_QT_EXCLUDE_PLUGINS}"
"$BITBOX_QT_BIN/windeployqt.exe" --qtpaths "$BITBOX_QT_BIN/qtpaths.exe" \
    --release --no-patchqt --no-compiler-runtime --no-system-d3d-compiler --exclude-plugins "$exclude_plugins" \
    --no-system-dxc-compiler "$windows/BitBox.exe"
cp "$BITBOX_QT_BIN/../plugins/position/qtposition_nmea.dll" "$windows/position/"
