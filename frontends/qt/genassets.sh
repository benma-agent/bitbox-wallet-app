# SPDX-License-Identifier: Apache-2.0

set -e

if [ ! -d ../web/build ]; then
    echo "Web assets have not been built." 1>&2
    exit 1
fi

build_dir=${1:-build}
mkdir -p "$build_dir"
qrc="$build_dir/assets.qrc"
{
    echo '<!DOCTYPE RCC><RCC version="1.0"><qresource>'
    find ../web/build/ -maxdepth 3 -type f | sort | sed -e "s|../web/build/||" | awk '{ print "<file alias=\"" $1 "\">../web/build/" $1 "</file>" }'
    echo '<file alias="trayicon.png">resources/trayicon.png</file>'
    echo '<file alias="trayicon-dark.png">resources/trayicon-dark.png</file>'
    echo '</qresource></RCC>'
} > "$qrc"
# On stdin, rcc resolves the resource paths relative to this source directory.
rcc -binary -o "$build_dir/assets.rcc" - < "$qrc"
cat "$qrc"
rm "$qrc"
