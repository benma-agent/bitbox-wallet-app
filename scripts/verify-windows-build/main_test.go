// SPDX-License-Identifier: Apache-2.0

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func releaseFiles() map[string]string {
	return map[string]string{
		"APP_VERSION":             "4.53.1\n",
		"icon.ico":                "icon",
		"setup.nsi":               "setup",
		"sign-windows-release.py": "sign",
		"windows/BitBox.exe":      "MZ\x00\xff",
		"windows/assets.rcc":      "assets",
		"windows/plugins/qt.dll":  "qt",
	}
}

func releaseFolder(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func releaseArchive(t *testing.T, files map[string]string, order []string, timestamp int64) []byte {
	t.Helper()
	var result bytes.Buffer
	gz := gzip.NewWriter(&result)
	gz.ModTime = time.Unix(timestamp, 0)
	writer := tar.NewWriter(gz)
	for _, name := range order {
		content := files[name]
		header := &tar.Header{Name: "./" + name, Mode: 0644, Size: int64(len(content)), ModTime: gz.ModTime}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return result.Bytes()
}

// A directory or differently encoded archive verifies when all relative paths and file bytes match.
func TestVerifyReleaseFiles(t *testing.T) {
	files := releaseFiles()
	local := releaseFolder(t, files)
	if err := verify(local, releaseFolder(t, files)); err != nil {
		t.Fatal(err)
	}
	names := slices.Sorted(maps.Keys(files))
	first := releaseArchive(t, files, names, 1234)
	slices.Reverse(names)
	second := releaseArchive(t, files, names, 5678)
	if bytes.Equal(first, second) {
		t.Fatal("expected different tar.gz bytes")
	}
	path := filepath.Join(t.TempDir(), "reference.tar.gz")
	for _, archive := range [][]byte{first, second} {
		if err := os.WriteFile(path, archive, 0600); err != nil {
			t.Fatal(err)
		}
		if err := verify(local, path); err != nil {
			t.Fatal(err)
		}
	}
}

// Report every changed, missing and extra file, including helpers and case-only path changes.
func TestVerifyReportsDifferences(t *testing.T) {
	files := releaseFiles()
	delete(files, "setup.nsi")
	local := releaseFolder(t, files)
	delete(files, "sign-windows-release.py")
	delete(files, "windows/BitBox.exe")
	files["windows/BITBOX.exe"] = "MZ\x00\xff"
	files["icon.ico"] = "changed"
	files["APP_VERSION"] = "4.53.2\n"
	files["windows/assets.rcc"] = "other assets"
	files["windows/plugins/qt.dll"] = "QT"
	files["extra"] = "extra"
	reference := releaseFolder(t, files)
	archive := filepath.Join(t.TempDir(), "reference.tar.gz")
	if err := os.WriteFile(archive, releaseArchive(t, files, slices.Sorted(maps.Keys(files)), 1234), 0600); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{reference, archive} {
		err := verify(local, source)
		for _, report := range []string{
			`Missing from both builds  "setup.nsi"`,
			`Missing from reference  "sign-windows-release.py"`,
			`Missing from reference  "windows/BitBox.exe"`,
			`Extra in reference  "windows/BITBOX.exe"`,
			`Changed  "APP_VERSION"`,
			`Changed  "icon.ico"`,
			`Changed  "windows/assets.rcc"`,
			`Changed  "windows/plugins/qt.dll"`,
			`Extra in reference  "extra"`,
		} {
			if err == nil || !strings.Contains(err.Error(), report) {
				t.Fatalf("missing %s in %v", report, err)
			}
		}
	}
}

// Reject duplicate members and damaged gzip trailers even if the first copy of every file matches.
func TestVerifyInvalidArchive(t *testing.T) {
	files := releaseFiles()
	names := slices.Sorted(maps.Keys(files))
	duplicate := releaseArchive(t, files, append(names, "windows/BitBox.exe"), 1234)
	corrupt := releaseArchive(t, files, names, 1234)
	corrupt[len(corrupt)-8] ^= 0xff // gzip CRC32
	for _, archive := range [][]byte{duplicate, corrupt} {
		if _, err := archiveHashes(bytes.NewReader(archive)); err == nil {
			t.Fatal("accepted invalid reference archive")
		}
	}
}
