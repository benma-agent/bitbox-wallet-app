# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

release=${1:?missing release folder}
reference=${2:?missing reference folder or archive}
if [[ ! -d $reference ]]; then
    unpacked=$(mktemp -d)
    trap 'rm -rf -- "$unpacked"' EXIT
    # Reading from stdin keeps tar from treating a Windows drive letter as a remote host.
    tar -xzf - --keep-old-files -C "$unpacked" < "$reference"
    reference=$unpacked
fi

diff -qr --binary --no-dereference -- "$release/" "$reference/"
