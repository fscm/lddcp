// -*- mode: go; coding: utf-8 -*-
//
// SPDX-FileCopyrightText: 2026 Frederico Martins
// SPDX-License-Identifier: GPL-3.0-only

/*
lddcp tests.
*/
package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

var minimalELF32 = []byte{
	0x7f, 'E', 'L', 'F', // e_ident: magic
	1,                   // EI_CLASS = ELFCLASS32
	1,                   // EI_DATA = ELFDATA2LSB
	1,                   // EI_VERSION
	0,                   // EI_OSABI
	0,                   // EI_ABIVERSION
	0, 0, 0, 0, 0, 0, 0, // EI_PAD
	2, 0, // e_type = ET_EXEC
	3, 0, // e_machine = EM_386
	1, 0, 0, 0, // e_version
	0, 0, 0, 0, // e_entry
	0, 0, 0, 0, // e_phoff
	0, 0, 0, 0, // e_shoff
	0, 0, 0, 0, // e_flags
	52, 0, // e_ehsize
	32, 0, // e_phentsize
	0, 0, // e_phnum
	40, 0, // e_shentsize
	0, 0, // e_shnum
	0, 0, // e_shstrndx
}

func dynamicSystemBinary(t *testing.T) string {
	t.Helper()
	candidates := []string{"/bin/sh", "/bin/ls", "/usr/bin/ls", "/bin/bash"}
	for _, candidate := range candidates {
		if interp, _, err := parseELF(candidate); err == nil && interp != "" {
			return candidate
		}
	}
	t.Skip("no dynamically-linked system ELF binary found")
	return ""
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "nested", "dst.bin")
	want := []byte("hello shared library")
	if err := os.WriteFile(src, want, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile() error: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("copied content: got %q wanted %q", got, want)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf(
			"copied file mode: got %v wanted %v",
			info.Mode().Perm(),
			os.FileMode(0o755),
		)
	}
}

func TestCopySymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real-target")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "link")
	if err := os.Symlink(target, src); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "nested", "link-copy")
	if err := copySymlink(src, dst); err != nil {
		t.Fatalf("copySymlink() error: %v", err)
	}
	got, err := os.Readlink(dst)
	if err != nil {
		t.Fatal(err)
	}
	if got != target {
		t.Errorf("copied symlink target: got %q wanted %q", got, target)
	}
	if err := copySymlink(src, dst); err != nil {
		t.Fatalf("copySymlink() overwrite error: %v", err)
	}
}

func TestDefaultLibPaths(t *testing.T) {
	if _, err := os.Stat(ldSoConf); err != nil {
		t.Skip("no /etc/ld.so.conf on this system")
	}
	paths := defaultLibPaths()
	if len(paths) == 0 {
		t.Fatal("expected at least one library search path")
	}
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if seen[path] {
			t.Errorf("duplicate path %q in result", path)
		}
		seen[path] = true
		if _, err := os.Stat(path); err != nil {
			t.Errorf("returned path %q does not exist: %v", path, err)
		}
	}
}

func TestDefaultLibPaths_DedupsFallbacks(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing")
	original := fallbackLibPaths
	fallbackLibPaths = []string{existing, existing, missing}
	defer func() { fallbackLibPaths = original }()
	paths := defaultLibPaths()
	count := 0
	for _, path := range paths {
		switch path {
		case existing:
			count++
		case missing:
			t.Errorf(
				"nonexistent fallback path %q should have been filtered out",
				path,
			)
		}
	}
	if count != 1 {
		t.Errorf(
			"fallback path %q appears %d time(s), expected 1 (dedup failed)",
			existing,
			count,
		)
	}
}

func TestFindLibrary(t *testing.T) {
	dir1, dir2 := t.TempDir(), t.TempDir()
	target := filepath.Join(dir2, "libfoo.so")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := findLibrary("libfoo.so", []string{dir1, dir2}); got != target {
		t.Errorf("findLibrary() error: got %q wanted %q", got, target)
	}
	if got := findLibrary("missing.so", []string{dir1, dir2}); got != "" {
		t.Errorf("findLibrary() error: got %q wanted empty string", got)
	}
}

func TestParseArgs_Success(t *testing.T) {
	origDestination := destination
	origPrograms := programs
	origLibraries := libraries
	defer func() {
		destination = origDestination
		programs = origPrograms
		libraries = origLibraries
	}()
	destination = ""
	programs = nil
	libraries = nil
	parseArgs(
		[]string{
			"-d",
			"/tmp/out",
			"-p",
			"/bin/ls",
			"-p",
			"/bin/sh",
			"-l",
			"libc.so.6",
		},
	)
	if destination != "/tmp/out" {
		t.Errorf("destination: got %q wanted %q", destination, "/tmp/out")
	}
	if want := []string{"/bin/ls", "/bin/sh"}; !slices.Equal(programs, want) {
		t.Errorf("programs: got %v wanted %v", programs, want)
	}
	if want := []string{"libc.so.6"}; !slices.Equal(libraries, want) {
		t.Errorf("libraries: got %v wanted %v", libraries, want)
	}
}

func TestParseArgs_Exit(t *testing.T) {
	if os.Getenv("LDDCP_PARSE_ARGS_HELPER") == "1" {
		args := os.Args[1:]
		for i, arg := range args {
			if arg == "--" {
				args = args[i+1:]
				break
			}
		}
		parseArgs(args)
		return
	}
	cases := []struct {
		name     string
		args     []string
		wantCode int
	}{
		{"help", []string{"-h"}, 0},
		{"version", []string{"-v"}, 0},
		{"missingDestArg", []string{"-d"}, 1},
		{"missingProgramArg", []string{"-p"}, 1},
		{"missingLibraryArg", []string{"-l"}, 1},
		{"invalidOption", []string{"-x"}, 1},
		{"missingDestination", []string{"-p", "/bin/ls"}, 2},
		{"missingProgramsAndLibraries", []string{"-d", "/tmp"}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmdArgs := append(
				[]string{"-test.run=TestParseArgs_Exit", "--"}, c.args...,
			)
			cmd := exec.Command(os.Args[0], cmdArgs...)
			cmd.Env = append(os.Environ(), "LDDCP_PARSE_ARGS_HELPER=1")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			err := cmd.Run()
			exitCode := 0
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else if err != nil {
				t.Fatalf("unexpected error running subprocess: %v", err)
			}
			if exitCode != c.wantCode {
				t.Errorf(
					"exit code: got %d wanted %d (stderr: %s)",
					exitCode,
					c.wantCode,
					stderr.String(),
				)
			}
		})
	}
}

func TestParseELF_DynamicBinary(t *testing.T) {
	binary := dynamicSystemBinary(t)
	interp, needed, err := parseELF(binary)
	if err != nil {
		t.Fatalf("parseELF() error: [%q] %v", binary, err)
	}
	if interp == "" {
		t.Errorf("expected a non-empty PT_INTERP for %q", binary)
	}
	if len(needed) == 0 {
		t.Errorf("expected at least one DT_NEEDED entry for %q", binary)
	}
}

func TestParseELF_NotELF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-elf.txt")
	if err := os.WriteFile(path, []byte("just some text"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := parseELF(path); err == nil {
		t.Fatal("expected an error for non-ELF file, got nil")
	}
}

func TestParseELF_NotFound(t *testing.T) {
	if _, _, err := parseELF(
		filepath.Join(t.TempDir(), "none.txt"),
	); err == nil {
		t.Fatal("expected an error for nonexistent file, got nil")
	}
}

func TestParseELF_SelfBinary(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	interp, needed, err := parseELF(self)
	if err != nil {
		t.Skipf("test binary could not be inspected as ELF: %v", err)
	}
	if (interp == "") != (len(needed) == 0) {
		t.Errorf("inconsistent result: interp=%q needed=%v", interp, needed)
	}
}

func TestParseELF_Supports32Bit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "elf32")
	if err := os.WriteFile(path, minimalELF32, 0o644); err != nil {
		t.Fatal(err)
	}
	interp, needed, err := parseELF(path)
	if err != nil {
		t.Fatalf("parseELF() error: [32-bit ELF file should be valid] %v", err)
	}
	if interp != "" {
		t.Errorf(
			"interp: [fixture has no program headers] got %q wanted empty",
			interp,
		)
	}
	if len(needed) != 0 {
		t.Errorf(
			"needed: [fixture has no dynamic section] got %v wanted none",
			needed,
		)
	}
}

func TestParseLdSoConf(t *testing.T) {
	dir := t.TempDir()
	libA := filepath.Join(dir, "libA")
	libB := filepath.Join(dir, "libB")
	missing := filepath.Join(dir, "does-not-exist")
	for _, d := range []string{libA, libB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	includedConf := filepath.Join(dir, "extra.conf")
	if err := os.WriteFile(includedConf, []byte(libB+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mainConf := filepath.Join(dir, "main.conf")
	content := "# a comment\n\n" +
		libA +
		"\n" +
		missing +
		"\ninclude " +
		includedConf +
		"\n"
	if err := os.WriteFile(mainConf, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got := parseLdSoConf(mainConf, make(map[string]bool))
	want := map[string]bool{libA: true, libB: true}
	if len(got) != len(want) {
		t.Fatalf("parseLdSoConf(): got %v wanted exactly %v", got, want)
	}
	for _, path := range got {
		if !want[path] {
			t.Errorf("unexpected path %q in result", path)
		}
	}
}

func TestResolveSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.WriteFile(real, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link2 := filepath.Join(dir, "link2") // link2 -> real.
	link1 := filepath.Join(dir, "link1") // link1 -> link2.
	if err := os.Symlink(real, link2); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(link2, link1); err != nil {
		t.Fatal(err)
	}
	gotReal, gotLinks := resolveSymlink(link1)
	if gotReal != real {
		t.Errorf("resolveSymlink() error: got %q wanted %q", gotReal, real)
	}
	wantLinks := []string{link1, link2}
	if len(gotLinks) != len(wantLinks) {
		t.Fatalf(
			"resolveSymlink() error: got %v wanted %v",
			gotLinks,
			wantLinks,
		)
	}
	for i := range wantLinks {
		if gotLinks[i] != wantLinks[i] {
			t.Errorf(
				"resolveSymlink() error: [%d] got %q wanted %q",
				i,
				gotLinks[i],
				wantLinks[i],
			)
		}
	}
	if path, links := resolveSymlink(real); path != real || len(links) != 0 {
		t.Errorf(
			"resolveSymlink() error: got (%q, %v) wanted (%q, [])",
			path,
			links,
			real,
		)
	}
}

func TestResolveSymlink_Cycle(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "self")
	if err := os.Symlink(self, self); err != nil {
		t.Fatal(err)
	}
	if _, links := resolveSymlink(self); len(links) == 0 {
		t.Error("expected self-referential symlink to be added before exiting")
	}
}

func TestScannerCopyEntry_Dedup(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src", "lib.so")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &scanner{destination: t.TempDir(), copied: make(map[string]bool)}
	if err := s.copyEntry(src); err != nil {
		t.Fatalf("copyEntry() error: %v", err)
	}
	if err := s.copyEntry(src); err != nil {
		t.Fatalf("copyEntry() second call error: %v", err)
	}
	if len(s.copied) != 1 {
		t.Errorf(
			"copied map has %d entries, wanted 1 (dedup failed)",
			len(s.copied),
		)
	}
}

func TestScannerParsePath_Integration(t *testing.T) {
	bin := dynamicSystemBinary(t)
	destination := t.TempDir()
	s := &scanner{
		destination: destination,
		searchPaths: defaultLibPaths(),
		visited:     make(map[string]bool),
		copied:      make(map[string]bool),
	}
	if err := s.parsePath(bin); err != nil {
		t.Fatalf("parsePath() error: [%q] %v", bin, err)
	}
	if len(s.copied) == 0 {
		t.Fatal("expected at least one file to be copied")
	}
	realPath, _ := resolveSymlink(bin)
	if _, err := os.Lstat(filepath.Join(destination, realPath)); err != nil {
		t.Errorf("expected %q itself to have been copied: %v", realPath, err)
	}
	interp, _, err := parseELF(realPath)
	if err != nil {
		t.Fatalf("parseELF() error: [%q] %v", realPath, err)
	}
	if _, err := os.Lstat(filepath.Join(destination, interp)); err != nil {
		t.Errorf(
			"expected interpreter %q to have been copied: %v",
			interp,
			err,
		)
	}
}

func TestScanneParsePath_InvalidELF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-elf.txt")
	if err := os.WriteFile(path, []byte("not an ELF file"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &scanner{
		destination: t.TempDir(),
		visited:     make(map[string]bool),
		copied:      make(map[string]bool),
	}
	if err := s.parsePath(path); err == nil {
		t.Fatal("expected an error for a non-ELF path, got nil")
	}
	if len(s.copied) != 0 {
		t.Errorf("expected nothing to be copied, got %v", s.copied)
	}
}

func TestScannerParsePath_SkipsAlreadyVisited(t *testing.T) {
	fakePath := "/does/not/exist"
	realPath, err := filepath.Abs(fakePath)
	if err != nil {
		t.Fatal(err)
	}
	s := &scanner{
		destination: t.TempDir(),
		visited:     map[string]bool{realPath: true},
		copied:      make(map[string]bool),
	}
	if err := s.parsePath(fakePath); err != nil {
		t.Errorf(
			"expected an already-visited path to be skipped silently: %v",
			err,
		)
	}
	if len(s.copied) != 0 {
		t.Errorf(
			"expected nothing to be copied for an already-visited path, got %v",
			s.copied,
		)
	}
}

func TestScannerParsePath_MissingLibraryWarnsButSucceeds(t *testing.T) {
	bin := dynamicSystemBinary(t)
	destination := t.TempDir()
	s := &scanner{
		destination: destination,
		searchPaths: nil, // guarantees no shared library.
		visited:     make(map[string]bool),
		copied:      make(map[string]bool),
	}
	if err := s.parsePath(bin); err != nil {
		t.Fatalf(
			"parsePath() error: got %v wanted nil "+
				"(missing deps should only warn)",
			err,
		)
	}
	realPath, _ := resolveSymlink(bin)
	if _, err := os.Lstat(filepath.Join(destination, realPath)); err != nil {
		t.Errorf("expected the binary itself to still be copied: %v", err)
	}
	interp, _, err := parseELF(realPath)
	if err != nil {
		t.Fatalf("parseELF() error: [%q] %v", realPath, err)
	}
	if _, err := os.Lstat(filepath.Join(destination, interp)); err != nil {
		t.Errorf("expected the interpreter to still be copied: %v", err)
	}
}

func TestScannerParsePath_PreservesSymlinkChainAndPath(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := parseELF(self); err != nil {
		t.Skipf("test binary is not a usable ELF file: %v", err)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "real-binary")
	if err := copyFile(self, target); err != nil {
		t.Fatalf("copyFile(): %v", err)
	}
	link2 := filepath.Join(dir, "link2") // link2 -> target.
	link1 := filepath.Join(dir, "link1") // link1 -> link2.
	if err := os.Symlink(target, link2); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(link2, link1); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	s := &scanner{
		destination: destination,
		visited:     make(map[string]bool),
		copied:      make(map[string]bool),
	}
	if err := s.parsePath(link1); err != nil {
		t.Fatalf("parsePath() error: %v", err)
	}
	for _, path := range []string{link1, link2, target} {
		if _, err := os.Lstat(filepath.Join(destination, path)); err != nil {
			t.Errorf(
				"expected %q to be copied preserving its absolute path: %v",
				path,
				err,
			)
		}
	}
	if got, err := os.Readlink(filepath.Join(destination, link1)); err != nil ||
		got != link2 {
		t.Errorf(
			"copied %q, got link target %q wanted target %q: %v",
			link1,
			got,
			link2,
			err,
		)
	}
}
