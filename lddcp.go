// -*- mode: go; coding: utf-8 -*-
//
// SPDX-FileCopyrightText: 2026 Frederico Martins
// SPDX-License-Identifier: GPL-3.0-only

/*
lddcp

Synopsis:

lddcp  recursively  searches  for  the shared libraries required by one or more
programs  (or  libraries) and copies them into a destination folder, preserving
the  full  absolute  path of each library relative to the root directory. It is
the  equivalent  of  running  'ldd'  and  then  manually  copying everything it
reports.

For  each  input,  lddcp parses the ELF headers to read the 'PT_INTERP' segment
(the  dynamic  linker)  and  the  'DT_NEEDED'  entries (the shared libraries it
depends  on).  Each  'DT_NEEDED' name is searched for in the directories parsed
from '/etc/ld.so.conf' and its included files, plus a set of standard multiarch
fallback paths.

If  the  resolved  path  of  a needed library is a symbolic link, lddcp records
every  intermediate  link  and  the  final  real  file.  Real  files are copied
byte-for-byte  and  symlinks  are  recreated  as  symlinks pointing to the same
target, so the destination reflects the on-disk layout of the source.

Every  newly  discovered library is itself scanned for its 'DT_NEEDED' entries,
which  are  also  processed.  lddcp  tracks  visited  paths  to  avoid rescans,
redundant copies, and dependency loops.

Usage:

lddcp [-d  directory] [-h] [-l  library]... [-p  program]... [-v]

-d directory

	Destination  directory  where  the  shared  libraries  will  be  copied to.
	Required.

-h  Show a help message and exit.

-l library

	Library  to  scan  for  shared  libraries.  The library itself will also be
	copied  to the destination folder. May be given several times. At least one
	of -p or -l is required.

-p program

	Program  to  scan  for  shared libraries. The program itself is not copied,
	only  its  dependencies are. May be given several times. At least one of -p
	or -l is required.

-v  Show the program's version number and exit.

Examples:

Copy  the dependencies of /bin/sh into /tmp/requirements (including the dynamic
linker):

	$ lddcp -p /bin/sh -d /tmp/requirements

Copy the dependencies of several programs using several -p options:

	$ lddcp -p /bin/sh -p /bin/ls -p /bin/ln -d /tmp/requirements

Copy  a  specific  library  and  its  dependencies,  for  example  to  bundle a
dependency that a program searches for at run time:

	$ lddcp -l libnss_dns.so.2 -d /tmp/requirements

Exit Status:

	0   Success. Also returned by -h and -v.
	1   Invalid option or a missing option argument.
	2   Missing required option (-d or at least one of -p/-l).
	3   The destination directory is invalid or could not be created.

Notes:

ldd  reports linux-vdso.so.1 (the virtual dynamic shared object injected by the
kernel),  which has no file on disk. lddcp silently skips any needed library it
cannot find on disk.

Libraries  are  looked  up  only  by  name in the configured search paths. If a
needed  library cannot be found, a warning is printed to standard error and the
scan continues.
*/
package main

import (
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	Author  string = "Frederico Martins"
	License string = "GPLv3"
	Version string = "0.2.0"

	header string = "%s version %s\nby %s under %s license\n\n"

	usage string = `Usage: %s [-d  directory] [-h] [-l  library]... [-p  program]... [-v]
  -d directory   Directory where the shared libraries will be copied to.
  -h	         Show this help message and exit.
  -l library     Library to scan for shared libraries (will also be copied).
  -p program     Program to scan for shared libraries.
  -v	         Show the program's version number and exit.
`

	ldSoConf string = "/etc/ld.so.conf"
)

var (
	name             string   = filepath.Base(os.Args[0])
	fallbackLibPaths []string = []string{
		"/lib",
		"/lib32",
		"/lib64",
		"/usr/lib",
		"/usr/lib32",
		"/usr/lib64",
		"/lib/i386-linux-gnu",
		"/usr/lib/i386-linux-gnu",
		"/lib/arm-linux-gnueabihf",
		"/usr/lib/arm-linux-gnueabihf",
		"/lib/x86_64-linux-gnu",
		"/usr/lib/x86_64-linux-gnu",
		"/lib/aarch64-linux-gnu",
		"/usr/lib/aarch64-linux-gnu",
	}

	programs    []string
	libraries   []string
	destination string
)

// scanner holds the state for the recursive library scan.
type scanner struct {
	visited     map[string]bool
	copied      map[string]bool
	destination string
	searchPaths []string
}

// copyEntry  copies  the  file  or symlink at src to the scanner's destination
// avoiding already copied items. Returns the first error encountered, if any.
func (s *scanner) copyEntry(src string) error {
	dst := filepath.Join(s.destination, src)
	if s.copied[dst] {
		return nil
	}
	s.copied[dst] = true
	fileInfo, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if fileInfo.Mode()&os.ModeSymlink != 0 {
		return copySymlink(src, dst)
	}
	return copyFile(src, dst)
}

// parsePath  resolves the path, copies all the encountered symlinks as well as
// the  real  file,  and  then recursively processes the ELF interpreter of the
// file  and  any  shared  libraries  it  depends  on.  Returns the first error
// encountered, if any.
func (s *scanner) parsePath(path string, copy bool) ([]string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("failed to get libraries for %q", path),
			err,
		)
	}
	realPath, links := resolveSymlink(absPath)
	if s.visited[realPath] {
		return nil, nil
	}
	s.visited[realPath] = true
	interp, needed, err := parseELF(realPath)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("invalid ELF file %q", realPath),
			err,
		)
	}
	if copy {
		for _, link := range links {
			if err := s.copyEntry(link); err != nil {
				fmt.Fprintf(os.Stderr, "unable to copy symlink %q\n", link)
			}
		}
		if err := s.copyEntry(realPath); err != nil {
			fmt.Fprintf(os.Stderr, "unable to copy file %q\n", realPath)
		}
	}
	if interp != "" {
		if _, err := s.parsePath(interp, true); err != nil {
			err = errors.Join(
				fmt.Errorf("invalid interpreter %q", interp),
				err,
			)
			fmt.Fprintf(os.Stderr, "%v\n", err)
		}
	}
	for _, libName := range needed {
		libPath := findLibrary(libName, s.searchPaths)
		if libPath == "" {
			fmt.Fprintf(
				os.Stderr,
				"library %q (needed by %s) not found\n",
				libName,
				realPath,
			)
			continue
		}
		if _, err := s.parsePath(libPath, true); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
		}
	}
	return needed, nil
}

// copyFile  copies  a  file  from  src to dst preserving the file's permission
// bits.  Creates the parent directories of dst (with mode 0755) if they do not
// exist. Returns the first error encountered, if any.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(
		dst,
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		info.Mode(),
	)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, in)
	return err
}

// copySymlink  copies  a  symbolic link from src to dst. Parent directories of
// dst  will be created (with mode 0755) if do not exist. If dst already exists
// it will be removed first. Returns the first error encountered, if any.
func copySymlink(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	target, err := os.Readlink(src)
	if err != nil {
		return err
	}
	if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.Symlink(target, dst)
}

// defaultLibPaths  returns the default set of directories to search for shared
// libraries.  It  will first read the system ld.so.conf configuration file and
// then  will  append  each  path  from  fallbackLibPaths  (if  exists  on  the
// filesystem) ignoring already appended ones.
func defaultLibPaths() []string {
	visited := make(map[string]bool)
	paths := parseLdSoConf(ldSoConf, visited)
	for _, path := range fallbackLibPaths {
		if !visited[path] {
			if _, err := os.Stat(path); err == nil {
				paths = append(paths, path)
			}
			visited[path] = true
		}
	}
	return paths
}

// findLibrary  returns  the  absolute  path  of  the  shared  library with the
// specified name if it exists in searchPaths or "" if it does not.
func findLibrary(name string, searchPaths []string) string {
	for _, dir := range searchPaths {
		candidate := filepath.Join(dir, name)
		if _, err := os.Lstat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// parseArgs parses the program arguments given at args.
func parseArgs(args []string) {
	argsLen := len(args)
	for i := 0; i < argsLen; i++ {
		switch args[i] {
		case "-d":
			i++
			if i >= argsLen {
				fmt.Fprintln(os.Stderr, "option '-d' requires an argument")
				os.Exit(1)
			}
			destination = args[i]
		case "-h":
			fmt.Printf(usage, name)
			os.Exit(0)
		case "-v":
			fmt.Println(Version)
			os.Exit(0)
		case "-p":
			i++
			if i >= argsLen {
				fmt.Fprintln(os.Stderr, "option '-p' requires an argument")
				os.Exit(1)
			}
			programs = append(programs, args[i])
		case "-l":
			i++
			if i >= argsLen {
				fmt.Fprintln(os.Stderr, "option '-l' requires an argument")
				os.Exit(1)
			}
			libraries = append(libraries, args[i])
		default:
			fmt.Fprintf(os.Stderr, "invalid option '%s'\n", args[i])
			os.Exit(1)
		}
	}
	if destination == "" {
		fmt.Fprintln(os.Stderr, "destination directory (-d) is required")
		os.Exit(2)
	}
	if len(programs) == 0 && len(libraries) == 0 {
		fmt.Fprintln(
			os.Stderr,
			"at least one program (-p) or library (-l) must be specified",
		)
		os.Exit(2)
	}
}

// parseELF  returns  the  information  of  the  ELF  file  at  path (PT_INTERP
// interpreter  and  the  DT_NEEDED  shared  library names) and the first error
// encountered, if any.
func parseELF(path string) (interp string, needed []string, err error) {
	file, err := elf.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = file.Close() }()
	if file.Class != elf.ELFCLASS64 && file.Class != elf.ELFCLASS32 {
		return "", nil, fmt.Errorf("unsupported ELF class %q", file.Class)
	}
	for _, header := range file.Progs {
		if header.Type != elf.PT_INTERP {
			continue
		}
		data, err := io.ReadAll(header.Open())
		if err != nil {
			return "", nil, errors.Join(
				errors.New("unable to read PT_INTERP segment"), err,
			)
		}
		interp = strings.TrimRight(string(data), "\x00")
		break
	}
	needed, err = file.ImportedLibraries() // nil, nil for static binaries.
	if err != nil {
		return "", nil, errors.Join(
			errors.New("unable to read DT_NEEDED entries"), err,
		)
	}
	return interp, needed, nil
}

// parseLdSoConf  reads  the  confFile  (ld.so.conf) and returns a deduplicated
// list  of  existing  library  search  paths.  Include directives are expanded
// recursively.
func parseLdSoConf(confFile string, visited map[string]bool) []string {
	if visited[confFile] {
		return nil
	}
	visited[confFile] = true
	data, err := os.ReadFile(confFile)
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if pattern, found := strings.CutPrefix(line, "include "); found {
			pattern = strings.TrimSpace(pattern)
			matches, _ := filepath.Glob(pattern)
			for _, match := range matches {
				paths = append(paths, parseLdSoConf(match, visited)...)
			}
			continue
		}
		if _, err := os.Stat(line); err == nil {
			paths = append(paths, line)
		}
	}
	return paths
}

// resolveSymlink  resolves all symlinks in the given path and returns the real
// absolute path as well as all of the intermediate link paths, in order.
func resolveSymlink(path string) (string, []string) {
	current := path
	visited := make(map[string]bool)
	links := []string{}
	for !visited[current] {
		visited[current] = true
		fileInfo, err := os.Lstat(current)
		if err != nil {
			return current, links
		}
		if fileInfo.Mode()&os.ModeSymlink == 0 {
			return current, links
		}
		links = append(links, current)
		target, err := os.Readlink(current)
		if err != nil {
			return current, links
		}
		if !filepath.IsAbs(target) {
			current = filepath.Clean(
				filepath.Join(filepath.Dir(current), target),
			)
		} else {
			current = filepath.Clean(target)
		}
	}
	return current, links
}

// main.
func main() {
	parseArgs(os.Args[1:])
	dest, err := filepath.Abs(destination)
	if err != nil {
		err = errors.Join(errors.New("invalid destination directory"), err)
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(3)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		err = errors.Join(
			errors.New("failed to create destination directory"),
			err,
		)
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(3)
	}
	destination = dest
	scanner := &scanner{
		destination: destination,
		searchPaths: defaultLibPaths(),
		visited:     make(map[string]bool),
		copied:      make(map[string]bool),
	}
	fmt.Printf(header, name, Version, Author, License)
	for _, program := range programs {
		fmt.Printf("scanning program: %q\n", program)
		needed, err := scanner.parsePath(program, false)
		if err != nil {
			fmt.Fprintf(
				os.Stderr,
				"%q is not a valid ELF file: %v\n",
				program,
				err,
			)
			continue
		}
		fmt.Printf("  direct dependencies: %v\n", needed)
	}
	for _, lib := range libraries {
		fmt.Printf("scanning library: %q\n", lib)
		if _, err := scanner.parsePath(lib, true); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
		}
	}
	fmt.Printf(
		"\nall done. %d file(s) copied to %q\n",
		len(scanner.copied),
		destination,
	)
}
