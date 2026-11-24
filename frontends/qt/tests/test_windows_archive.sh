#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
writer=$(cd "$(dirname "$0")/.." && pwd)/archive_windows.sh
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
export SOURCE_DATE_EPOCH=1700000000

# Different checkout locations, creation order, file modes and times must produce identical bytes.
for tree in first second; do
    bundle="$work/$tree"
    mkdir -p "$bundle/windows/plugins"
    echo 4.53.1 > "$bundle/APP_VERSION"
    for file in icon.ico setup.nsi sign-windows-release.py; do echo "$file" > "$bundle/$file"; done
    files=(BitBox.exe plugins/a.dll plugins/z.dll)
    if [[ $tree == second ]]; then files=(plugins/z.dll plugins/a.dll BitBox.exe); fi
    for file in "${files[@]}"; do echo "$file" > "$bundle/windows/$file"; done
    if [[ $tree == first ]]; then
        chmod +x "$bundle/windows/BitBox.exe"
        touch -d @1000000000 "$bundle/windows/"{BitBox.exe,plugins}
    fi
    bash "$writer" "$bundle" "$work/$tree.tar.gz"
done
cmp "$work/first.tar.gz" "$work/second.tar.gz"
diff -u <(printf '%s\n' APP_VERSION icon.ico setup.nsi sign-windows-release.py windows/ \
    windows/BitBox.exe windows/plugins/ windows/plugins/a.dll windows/plugins/z.dll) \
    <(tar -tzf "$work/first.tar.gz")

# A changed payload byte must still change the archive, even with all metadata normalized.
echo changed >> "$bundle/windows/plugins/a.dll"
bash "$writer" "$bundle" "$work/changed.tar.gz"
if cmp -s "$work/second.tar.gz" "$work/changed.tar.gz"; then exit 1; fi
