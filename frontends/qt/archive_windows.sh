# SPDX-License-Identifier: Apache-2.0

set -euo pipefail
export LC_ALL=C

# Archive the same flat directory that the verifier compares. The caller publishes
# the output only after this pipeline succeeds. Only directories get execute bits.
(
    cd "${1:?missing release directory}"
    shopt -s dotglob
    tar --format=gnu --sort=name --owner=0 --group=0 --numeric-owner --hard-dereference \
        --mode='a=r,u+w,a+X' --mtime="@${SOURCE_DATE_EPOCH:?missing commit timestamp}" -cf - -- *
) | gzip -n -6 > "${2:?missing output path}"
