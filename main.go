// -*- mode: go; coding: utf-8 -*-
//
// SPDX-FileCopyrightText: 2026 Frederico Martins
// SPDX-License-Identifier: GPL-3.0-only

/*
lddcp

Synopsis:

lddcp  recursively  search  for  any  shared  libraries required by one or more
programs  (or  libraries)  and  will  copy  those  into  a  destination  folder
preserving  the  full  absolute path of each library relative to '/'. It is the
equivalent of running 'ldd' and then manually copying everything it reports.

lddcp  will parse ELF binary's headers to read the 'PT_INTERP' (dynamic linker)
and the 'DT_NEEDED' (dynamic entries) segments.

Each   'DT_NEEDED'   name   is   searched   in   the  directories  parsed  from
'/etc/ld.so.conf'  and  its includes, plus a set of standard multiarch fallback
paths.

If  the  resolved path of the 'DT_NEEDED' entry is a symlink, lddcp will record
all intermediate links and the final real file.

Real  files  will  be  copied  byte-for-byte  and symlinks will be recreated as
symlinks pointing to the same target.

Every  newly  discovered  library  will  itself  be scanned for its 'DT_NEEDED'
entries  that  will  also  be  processed.  lddcp  will  keep track of processed
libraries  to avoid those that were already processed as well as any dependency
loops.

Usage:

lddcp -d <DIRECTORY> [-h] [-l <LIBRARY>] [-p <PROGRAM>] [-v]

-d <DIRECTORY> Folder to where the shared libraries will be copied to.
-h             Show this help message and exit.
-l <LIBRARY>   Library to scan for shared libraries (will also be copied).
-p <PROGRAM>   Program to scan for shared libraries.
-v             Show program's version number and exit.

Examples:

The following example will check the 'sh' program for shared libraries and copy
them to the '/tmp/requirements' folder:

lddcp -p /bin/sh -d /tmp/requirements

Checking  several  programs  is also possible. The following example will check
the 'sh', the 'ls' and the 'ln' programs using several '-p' options:

lddcp -p /bin/sh -p /bin/ls -p /bin/ln -d /tmp/requirements
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
	Version string = "0.1.0"

	header string = "%s version %s\nby %s under %s license\n\n"

	usageHelp string = `Usage: %s -d <DIRECTORY> [-h] [-l <LIBRARY>] [-p <PROGRAM>] [-v]
  -d <DIRECTORY> Folder to where the shared libraries will be copied to.
  -h	         Show this help message and exit.
  -l <LIBRARY>   Library to scan for shared libraries (will also be copied).
  -p <PROGRAM>   Program to scan for shared libraries.
  -v	         Show program's version number and exit.
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

// elfInfo  holds  the  information  extracted  from  an  ELF  file  (PT_INTERP
// interpreter, and shared libraries).
type elfInfo struct {
	interp string
	needed []string
}

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
func (s *scanner) parsePath(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	realPath, links := resolveSymlink(absPath)
	if s.visited[realPath] {
		return nil
	}
	s.visited[realPath] = true
	info, err := parseELF(realPath)
	if err != nil {
		return errors.Join(fmt.Errorf("invalid ELF file %q", realPath), err)
	}
	for _, link := range links {
		if err := s.copyEntry(link); err != nil {
			fmt.Fprintf(os.Stderr, "unable to copy symlink %q\n", link)
		}
	}
	if err := s.copyEntry(realPath); err != nil {
		fmt.Fprintf(os.Stderr, "unable to copy file %q\n", realPath)
	}
	if info.interp != "" {
		if err := s.parsePath(info.interp); err != nil {
			err = errors.Join(
				fmt.Errorf("invalid interpreter %q", info.interp),
				err,
			)
			fmt.Fprintf(os.Stderr, "%v\n", err)
		}
	}
	for _, libName := range info.needed {
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
		if err := s.parsePath(libPath); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
		}
	}
	return nil
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
	paths := parseLdSoConf(ldSoConf, make(map[string]bool))
	parsed := make(map[string]bool, len(paths))
	for _, path := range paths {
		parsed[path] = true
	}
	for _, path := range fallbackLibPaths {
		if !parsed[path] {
			if _, err := os.Stat(path); err == nil {
				paths = append(paths, path)
			}
			parsed[path] = true
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
			fmt.Printf(usageHelp, name)
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
func parseELF(path string) (*elfInfo, error) {
	file, err := elf.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	if file.Class != elf.ELFCLASS64 && file.Class != elf.ELFCLASS32 {
		return nil, fmt.Errorf("unsupported ELF class %q", file.Class)
	}
	info := &elfInfo{}
	for _, header := range file.Progs {
		if header.Type != elf.PT_INTERP {
			continue
		}
		data, err := io.ReadAll(header.Open())
		if err != nil {
			return nil, errors.Join(
				errors.New("unable to read PT_INTERP segment"), err,
			)
		}
		info.interp = strings.TrimRight(string(data), "\x00")
		break
	}
	needed, err := file.ImportedLibraries() // nil, nil for static binaries.
	if err != nil {
		return nil, errors.Join(
			errors.New("unable to read DT_NEEDED entries"), err,
		)
	}
	info.needed = needed
	return info, nil
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
	// for line := range strings.SplitSeq(string(data), "\n") {
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
		absPath, err := filepath.Abs(program)
		if err != nil {
			err = errors.Join(
				fmt.Errorf("failed to get libraries for %q", program),
				err,
			)
			fmt.Fprintf(os.Stderr, "%v\n", err)
			continue
		}
		realPath, _ := resolveSymlink(absPath)
		info, err := parseELF(realPath)
		if err != nil {
			fmt.Fprintf(
				os.Stderr,
				"%q is not a valid ELF file: %v\n",
				program,
				err,
			)
			continue
		}
		scanner.visited[realPath] = true
		if info.interp != "" {
			if err := scanner.parsePath(info.interp); err != nil {
				err = errors.Join(
					fmt.Errorf("invalid interpreter %q", info.interp),
					err,
				)
				fmt.Fprintf(os.Stderr, "%v\n", err)
			}
		}
		for _, libName := range info.needed {
			libPath := findLibrary(libName, scanner.searchPaths)
			if libPath == "" {
				fmt.Fprintf(
					os.Stderr,
					"library %q not found (needed by %q)\n",
					libName,
					program,
				)
				continue
			}
			if err := scanner.parsePath(libPath); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
			}
		}
		fmt.Printf("  direct dependencies: %v\n", info.needed)
	}
	for _, lib := range libraries {
		fmt.Printf("scanning library: %q\n", lib)
		if err := scanner.parsePath(lib); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
		}
	}
	fmt.Printf(
		"\nall done. %d file(s) copied to %q\n",
		len(scanner.copied),
		destination,
	)
}
