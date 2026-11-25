#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
qt=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
export SOURCE_DATE_EPOCH=1700000000

# Different checkout locations, creation order, file modes and times must produce identical bytes.
for tree in first second; do
    checkout="$work/$tree checkout"
    project="$checkout/frontends/qt"
    build="$work/$tree native build"
    mkdir -p "$project/resources/win" "$build/windows/plugins"
    cp "$qt/archive_windows.sh" "$project/"
    echo 4.53.1 > "$checkout/APP_VERSION"
    echo icon > "$project/resources/win/icon.ico"
    for file in setup.nsi sign-windows-release.py; do echo "$file" > "$project/$file"; done
    # Only the application directory is archived, excluding compiler intermediates.
    echo intermediate > "$build/libserver.lib"
    files=(BitBox.exe plugins/a.dll plugins/z.dll)
    if [[ $tree == second ]]; then files=(plugins/z.dll plugins/a.dll BitBox.exe); fi
    for file in "${files[@]}"; do printf 'MZ\0%s\n' "$file" > "$build/windows/$file"; done
    if [[ $tree == first ]]; then
        chmod +x "$build/windows/BitBox.exe" "$project/setup.nsi"
        touch -d @1000000000 "$build/windows/"{BitBox.exe,plugins} "$checkout/APP_VERSION"
    fi
    bash "$project/archive_windows.sh" "$build" "$work/$tree.tar.gz"
done
cmp "$work/first.tar.gz" "$work/second.tar.gz"
diff -u <(printf '%s\n' APP_VERSION icon.ico setup.nsi sign-windows-release.py windows/ \
    windows/BitBox.exe windows/plugins/ windows/plugins/a.dll windows/plugins/z.dll) \
    <(tar -tzf "$work/first.tar.gz")

# A failed archive operation must preserve an existing published archive and remove its temporary file.
printf 'previous archive' > "$work/published.tar.gz"
mkdir "$work/missing"
if bash "$qt/archive_windows.sh" "$work/missing" "$work/published.tar.gz"; then exit 1; fi
[[ $(cat "$work/published.tar.gz") == 'previous archive' ]]
[[ -z $(find "$work" -name 'published.tar.gz.*') ]]
