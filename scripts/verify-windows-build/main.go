// SPDX-License-Identifier: Apache-2.0

// verify-windows-build compares an unpacked release with another folder or a CI tar.gz.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
)

type fileHashes map[string][sha256.Size]byte

func (files fileHashes) add(name string, contents io.Reader) error {
	if _, exists := files[name]; exists {
		return fmt.Errorf("duplicate file %q", name)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, contents); err != nil {
		return fmt.Errorf("reading %q: %w", name, err)
	}
	files[name] = [sha256.Size]byte(hash.Sum(nil))
	return nil
}

func directoryHashes(directory string) (fileHashes, error) {
	root := os.DirFS(directory)
	files := fileHashes{}
	err := fs.WalkDir(root, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if !entry.Type().IsRegular() || name == "." {
			return fmt.Errorf("expected an ordinary release file at %q", name)
		}
		file, err := root.Open(name)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		return files.add(name, file)
	})
	return files, err
}

func archiveHashes(file io.Reader) (fileHashes, error) {
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer func() { _ = compressed.Close() }()
	reader := tar.NewReader(compressed)
	files := fileHashes{}
	dirs := map[string]bool{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag == tar.TypeDir && (header.Name == "." || header.Name == "./") {
			continue
		}
		name := strings.TrimPrefix(header.Name, "./")
		if header.Typeflag == tar.TypeDir {
			name = strings.TrimSuffix(name, "/")
		}
		if !fs.ValidPath(name) || name == "." || strings.ContainsAny(name, `\:`) {
			return nil, fmt.Errorf("invalid archive path %q", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if _, exists := files[name]; exists {
				return nil, fmt.Errorf("%q is both a file and a directory", name)
			}
			dirs[name] = true
		case tar.TypeReg:
			if dirs[name] {
				return nil, fmt.Errorf("%q is both a file and a directory", name)
			}
			if err := files.add(name, reader); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unsupported tar entry %q (type %q)", name, header.Typeflag)
		}
	}
	// Read through tar's zero padding to check the gzip checksum and detect trailing members.
	var padding [32 * 1024]byte
	for {
		n, err := compressed.Read(padding[:])
		if len(bytes.Trim(padding[:n], "\x00")) != 0 {
			return nil, errors.New("unexpected data after the end of the tar archive")
		}
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func referenceHashes(reference string) (fileHashes, error) {
	info, err := os.Stat(reference)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return directoryHashes(reference)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("expected a release folder or tar.gz at %q", reference)
	}
	file, err := os.Open(reference)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return archiveHashes(file)
}

func verify(local, reference string) error {
	built, err := directoryHashes(local)
	if err != nil {
		return fmt.Errorf("local build %q: %w", local, err)
	}
	other, err := referenceHashes(reference)
	if err != nil {
		return fmt.Errorf("reference %q: %w", reference, err)
	}
	var differences []string
	for _, name := range []string{"APP_VERSION", "icon.ico", "setup.nsi", "sign-windows-release.py", "windows/BitBox.exe"} {
		_, inLocal := built[name]
		_, inReference := other[name]
		if !inLocal && !inReference {
			differences = append(differences, fmt.Sprintf("Missing from both builds  %q", name))
		}
	}
	for name, hash := range built {
		match, exists := other[name]
		if !exists {
			differences = append(differences, fmt.Sprintf("Missing from reference  %q", name))
		} else if match != hash {
			differences = append(differences, fmt.Sprintf("Changed  %q", name))
		}
	}
	for name := range other {
		if _, exists := built[name]; !exists {
			differences = append(differences, fmt.Sprintf("Extra in reference  %q", name))
		}
	}
	if len(differences) > 0 {
		slices.Sort(differences)
		return fmt.Errorf("release verification failed\n%s", strings.Join(differences, "\n"))
	}
	fmt.Printf("Verified %d release files against %s\n", len(built), reference)
	return nil
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "Usage: verify-windows-build LOCAL_FOLDER REFERENCE_FOLDER_OR_TAR_GZ")
		os.Exit(2)
	}
	if err := verify(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
