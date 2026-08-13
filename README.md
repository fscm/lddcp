# lddcp

`lddcp` recursively searches for the shared libraries required by one or more
programs (or libraries) and copies them into a destination folder, preserving
the full absolute path of each library relative to the root directory. It is
the equivalent of running `ldd` and then manually copying everything it reports.

## Synopsis

For each input, `lddcp` parses the ELF headers to read the `PT_INTERP` segment
(the dynamic linker) and the `DT_NEEDED` entries (the shared libraries it
depends on). Each `DT_NEEDED` name is searched for in the directories parsed
from `/etc/ld.so.conf` and its included files, plus a set of standard multiarch
fallback paths.

If the resolved path of a needed library is a symbolic link, `lddcp` records
every intermediate link and the final real file. Real files are copied
byte-for-byte and symlinks are recreated as symlinks pointing to the same
target, so the destination reflects the on-disk layout of the source.

Every newly discovered library is itself scanned for its `DT_NEEDED` entries,
which are also processed. `lddcp` tracks visited paths to avoid rescans,
redundant copies, and dependency loops.

### Note (`linux-vdso.so.1`)

`ldd` reports `linux-vdso.so.1` (the virtual dynamic shared object injected by
the kernel), which has no file on disk. `lddcp` silently skips any needed
library it cannot find on disk.

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

* `-d <directory>` - *[required]* Directory where the shared libraries will be
  copied to.
* `-h` - Show a help message and exit.
* `-l <library>` - Library to scan for shared libraries. The library itself
  will also be copied to the destination folder. May be given several times.
  At least one of `-p` or `-l` is required.
* `-p <program>` - *[required]* Program to scan for shared libraries. The
  program itself is not copied, only its dependencies are. May be given several
  times. At least one of `-p` or `-l` is required.
* `-v` - Show the program's version number and exit.

### Examples

Copy the dependencies of */bin/sh* into */tmp/requirements* (including the
dynamic linker):

```
lddcp -p /bin/sh -d /tmp/requirements
```

Copy the dependencies of several programs using several -p options:

```
lddcp -p /bin/sh -p /bin/ls -p /bin/ln -d /tmp/requirements
```

Copy a specific library and its dependencies, for example to bundle a
dependency that a program searches for at run time:

```
lddcp -p /bin/sh -p /bin/ls -p /bin/ln -d /tmp/requirements
```

## Supported Platforms

`lddcp` reads the Linux ELF ABI and `/etc/ld.so.conf`, so it only makes sense
to run it on Linux. `lddcp` binaries are statically linked and have no runtime
dependencies.

| OS    | Architecture |
|-------|--------------|
| Linux | x86          |
| Linux | x86\_64      |
| Linux | arm64        |

## Build (from source)

Golang (version 1.21.0 or above) needs to be installed on your local computer.
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
