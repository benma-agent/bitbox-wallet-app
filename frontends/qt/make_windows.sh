# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

output=${1:-installer}
if [[ "$output" != installer && "$output" != payload ]]; then
    echo "Usage: $0 [installer|payload]" >&2
    exit 1
fi
if [[ "$output" == payload ]]; then
    : "${BITBOX_QT_BIN:?Use make qt-windows-archive to select the release toolchain}"
    : "${SOURCE_DATE_EPOCH:?missing release commit timestamp}"
fi

# windeployqt can leave files from earlier deployments in the output directory.
rm -rf build/windows
mkdir -p build/windows
bash ./genassets.sh
"${BITBOX_WINDOWS_MAKE:-make}" -C server/ windows-legacy
MSYS2_ARG_CONV_EXCL='*' env -u MAKE -u MAKEFLAGS cmd.exe /d /c compile_windows.bat
cp build/assets.rcc build/windows/
cp server/libserver.dll build/windows/
cp ../../vendor/github.com/breez/breez-sdk-spark-go/breez_sdk_spark/lib/windows-amd64/breez_sdk_spark_bindings.dll build/windows/
if [[ -n ${BITBOX_QT_BIN:-} ]]; then
    # The Qt kit supplies its own Microsoft D3D compiler. Host Windows versions can differ.
    cp "$BITBOX_QT_BIN/d3dcompiler_47.dll" build/windows/D3Dcompiler_47.dll
    # The release includes Qt Positioning's NMEA plug-in. Deploy that file directly
    # because scanning it also pulls in Qt Serial Port on kits with that optional module.
    exclude_plugins="qtposition_nmea${BITBOX_QT_EXCLUDE_PLUGINS:+,$BITBOX_QT_EXCLUDE_PLUGINS}"
    "$BITBOX_QT_BIN/windeployqt.exe" --qtpaths "$BITBOX_QT_BIN/qtpaths.exe" \
        --release --no-patchqt --no-compiler-runtime --no-system-d3d-compiler --exclude-plugins "$exclude_plugins" \
        --no-system-dxc-compiler build/windows/BitBox.exe
    cp "$BITBOX_QT_BIN/../plugins/position/qtposition_nmea.dll" build/windows/position/
else
    windeployqt build/windows/BitBox.exe
fi

if [[ "$output" == payload ]]; then
    exit 0
fi

APP_VERSION="$(tr -d '\r\n' < ../../APP_VERSION)"
makensis -DVERSION="${APP_VERSION}.0" \
    -DBINDIR='build\windows' \
    -DICONFILE='resources\win\icon.ico' \
    setup.nsi
