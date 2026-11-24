// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Build the actual field packages so changes to Go's assembler arguments or gnark-crypto's
// include layout cannot silently reintroduce checkout paths into release binaries.
func TestSharedAssemblyPaths(t *testing.T) {
	tools := t.TempDir()
	output, err := exec.Command("go", "build", "-mod=vendor", "-o", tools+string(os.PathSeparator), ".").CombinedOutput()
	require.NoError(t, err, "%s", output)
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CGO_ENABLED", "0")

	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			t.Setenv("GOARCH", arch)
			for field, element := range map[string]string{"fp": "element_6w", "fr": "element_4w"} {
				object := filepath.Join(t.TempDir(), field+".a")
				pkg := module + "/ecc/bls12-381/" + field
				output, err := exec.Command("go", "build", "-trimpath", "-mod=vendor",
					"-toolexec=trim-go-asm", "-o", object, pkg).CombinedOutput()
				require.NoError(t, err, "%s", output)
				data, err := os.ReadFile(object)
				require.NoError(t, err)
				source := module + "/field/asm/" + element + "/" + element + "_" + arch + ".s"
				require.True(t, bytes.Contains(data, []byte(source)), "missing assembly filename %s", source)
				require.False(t, bytes.Contains(data, []byte("/"+source)), "absolute assembly filename for %s", source)
			}
		})
	}
}
