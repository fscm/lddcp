# lddcp

`lddcp` recursively search for any shared libraries required by one or more
programs (or libraries) and will copy those into a destination folder
preserving the full absolute path of each library relative to `/`. It is the
equivalent of running `ldd` and then manually copying everything it reports.

## Synopsis

`lddcp` will parse ELF binary's headers to read the `PT_INTERP` (dynamic
linker) and the `DT_NEEDED` (dynamic entries) segments.

Each `DT_NEEDED` name is searched in the directories parsed from
`/etc/ld.so.conf` and its includes, plus a set of standard multiarch fallback
paths.

If the resolved path of the `DT_NEEDED` entry is a symlink, `lddcp` will record
all intermediate links and the final real file.

Real files will be copied byte-for-byte and symlinks will be recreated as
symlinks pointing to the same target.

Every newly discovered library will itself be scanned for its `DT_NEEDED`
entries that will also be processed. `lddcp` will keep track of processed
libraries to avoid those that were already processed as well as any dependency
loops.

### Note (`linux-vdso.so.1`)

`ldd` reports `linux-vdso.so.1` (the virtual dynamic shared object injected by
the kernel). This is not a file on disk (it has no path) so `lddcp` silently
skips it.

### Features

- Scans programs and extra libraries for ELF shared-library dependencies
  (finding the same set that `ldd` would report).
- Recursively scans every discovered library for its dependencies.
- Copies the dynamic linker / interpreter (`PT_INTERP`) automatically.
- Preserves 'symbolic links' as links. The real file each link points to is
  also copied.
- Keeps the full path of every library (e.g. a library at
  `/lib/x86_64-linux-gnu/libc.so.6` will be placed at
  `<dest>/lib/x86_64-linux-gnu/libc.so.6`).
- Keeps a list of visited paths to avoid loops and redundant copies.
- Zero external dependencies (no shell-out to `ldd`, `objdump`, or anything
  else).
- Reads `/etc/ld.so.conf` (and all its `include` files) to discover library
  search paths.
- Cross-compiled to a static binary for both `linux/amd64` and `linux/arm64`.

## Usage

In order to run `lddcp` you need to provide a few options.

```
lddcp [options]
```

### Program Options

* `-d <DIRECTORY>` - *[required]* Folder to where the shared libraries will be
  copied to.
* `-h` - Show the help message and exit.
* `-l <LIBRARY>` - Library to scan for shared libraries. Several Libraries can
  be set using extra `-l` options. The library will also be copied to the
  destination folder.
* `-p <PROGRAM>` - *[required]* Program to scan for shared libraries. Several
  programs can be set using extra `-p` options.
* `-v` - Show program's version number and exit.

### Examples

The following example will check the *sh* program for shared libraries and copy
them to the */tmp/requirements* folder:

```
lddcp -p /bin/sh -d /tmp/requirements
```

Checking several programs is also possible. The following example will check
the *sh*, the *ls* and the *ln* programs using several `-p` options:

```
lddcp -p /bin/sh -p /bin/ls -p /bin/ln -d /tmp/requirements
```

## Supported Platforms

`lddcp` reads the Linux ELF ABI and `/etc/ld.so.conf`, so it only makes sense
to run it on Linux. The binaries are statically linked and have no runtime
dependencies.

| OS    | Architecture |
|-------|--------------|
| Linux | x86          |
| Linux | x86\_64      |
| Linux | arm64        |

## Build (from source)

Golang (version 1.20.0 or above) needs to be installed on your local computer.
Golang setup can be found at [go.dev](https://go.dev).

Just (version 1.46.0 or above) needs to be installed on your local computer.

Just is used to automate several steps of the development process. Just setup
can be found at [just.systems](https://just.systems).

All of the commands described bellow are to be executed on the root folder
of this project.

To build the `lddcp` binaries use the following command:

```shell
just build-all
```

To create distribution archives of the `lddcp` program use the following
command:

```shell
just dist-all
```

## Contributing

1. Fork it!
2. Create your feature branch: `git checkout -b my-new-feature`
3. Commit your changes: `git commit -am 'Add some feature'`
4. Push to the branch: `git push origin my-new-feature`
5. Submit a pull request

## Versioning

This project uses [SemVer](http://semver.org/) for versioning. For the versions
available, see the [tags](https://github.com/fscm/lddcp/tags) on this repository.

## Authors

* **Frederico Martins** - [fscm](https://github.com/fscm)

See also the list of [contributors](https://github.com/fscm/lddcp/contributors)
who participated in this project.

## License

This project is licensed under the GPLv3 License - see the [LICENSE](LICENSE)
file for details
