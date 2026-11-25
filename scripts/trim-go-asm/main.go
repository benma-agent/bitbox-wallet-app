// SPDX-License-Identifier: Apache-2.0

// Command trim-go-asm adds a source path rewrite to the Go assembler for gnark-crypto.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const module = "github.com/consensys/gnark-crypto"

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("expected a Go tool and its arguments")
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin, cmd.Stderr = os.Stdin, os.Stderr
	if strings.TrimSuffix(filepath.Base(args[0]), ".exe") == "asm" {
		if len(args) == 2 && args[1] == "-V=full" {
			// Go uses this version in its cache key. Bump the suffix if the rewrite changes.
			version, err := cmd.Output()
			if err != nil {
				return err
			}
			_, err = fmt.Printf("%s-bitbox-gnark-path-v1\n", strings.TrimSpace(string(version)))
			return err
		}
		pkg, _, _ := strings.Cut(os.Getenv("TOOLEXEC_IMPORTPATH"), " ")
		if pkg == module+"/ecc/bls12-381/fp" || pkg == module+"/ecc/bls12-381/fr" {
			// These packages include ../../../field/asm sources on amd64 and arm64.
			// Go's default -trimpath covers the package and object directories. Add the
			// shared directory here so the host path stays out of go build's recorded flags.
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			rule := filepath.Join(dir, "..", "..", "..", "field", "asm") + "=>" + module + "/field/asm;"
			for i := 1; i < len(cmd.Args); i++ {
				arg := cmd.Args[i]
				switch {
				case arg == "-trimpath" && i+1 < len(cmd.Args):
					cmd.Args[i+1] = rule + cmd.Args[i+1]
				case strings.HasPrefix(arg, "-trimpath="):
					cmd.Args[i] = "-trimpath=" + rule + strings.TrimPrefix(arg, "-trimpath=")
				}
			}
		}
	}
	cmd.Stdout = os.Stdout
	return cmd.Run()
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
