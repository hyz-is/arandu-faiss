---
name: faiss-release
description: Get this Arandu package green, declared and published. Use when the request is to "run the gates", "check it builds", "the tests fail", "it does not build without cgo", "go build says the directory prefix does not contain modules listed in go.work", "aru doctor says this is not an Arandu project", "faiss/c_api not found", "update arandu.mod.toml", "add a dependency", "bump the framework", "move to another FAISS version", "tag a release", "publish it", "go get cannot find the module", "write the changelog", or before opening a pull request. Covers the gates and why each one is there, CGO_ENABLED and FAISS, what the manifest declares and who checks it, the immutability of a published module version, and what breaks somebody else's build.
license: MIT
---

# Getting it green and out

## The gates

Nothing is finished until all of them exit zero.

```sh
export GOWORK=off CGO_ENABLED=1
gofmt -l $(find . -name '*.go' -not -path '*/testdata/*' -not -name '*.kyse.go')
go build ./...
CGO_ENABLED=0 go build ./...
go vet ./...
go test -race -count=1 ./...
CGO_ENABLED=0 go test -count=1 ./...
```

`GOWORK=off` first, and here it is not a habit. This checkout may sit beside a
Go workspace that lists the framework repositories and does not list this one.
When it does, `go build ./...` exits 1 before compiling anything:

```
pattern ./...: directory prefix . does not contain modules listed in go.work
or their selected dependencies
```

With the workspace off, the module resolves the versions in `go.mod` — which is
what CI compiles against, and what somebody's `go get` will get.

`CGO_ENABLED=1` is the real build: the binding is cgo over FAISS's C API, and
FAISS has to be installed with it (`brew install faiss`; on Linux, built from
source, as the README shows). The tests that need FAISS are behind the
`cgo && !nofaiss` constraint and do not skip, so a green cgo run is a run
against FAISS. The `CGO_ENABLED=0` lines are the stub: it compiles, and its own
tests assert `ErrUnavailable` everywhere. `-race` needs cgo, so the stub's run
is without it.

`aru doctor` is not a gate. It exits 1 here with *"this is not an Arandu
project: no go.mod, main.go and arandu.toml together"* — it reads applications,
and this is a library.

CI runs the cgo gates on macOS with Homebrew's FAISS, the stub gates on Linux,
and the cgo gates on Linux against FAISS built from source. The release
workflow runs the cgo gates on the exact tag archive, on macOS.

## What the manifest declares, and who reads it

`arandu.mod.toml` is a promise, and the check that gives it weight runs here.
Nowhere else does: `aru doctor` audits the tree of the application it is run
inside and never loads a dependency, so the manifest of an installed package is
opened by nothing but that package's own suite.

```toml
name = "hyz-is/faiss"
framework = ">= 0.56"
profiles = ["conventional", "performance"]

[permissions]
network = false
filesystem = false
exec = false
migrations = false
```

`TestTheDeclaredCapabilitiesAreWhatTheCodeDoes`, in `tests/Unit/audit_test.go`,
compares the four permissions against what the code *calls*, by AST, in every
file either build compiles — the cgo binding and the stub alike;
`TestTheAuditReadsBothBuilds` fails if one of them drops out. `filesystem =
false` holds because serialization goes through `open_memstream` and
`fmemopen`, in memory, and the bytes go to the application's `Store`. The cgo
link to FAISS has no key in the manifest, so the comment under `exec` says it.

So a call added here that reaches the network, a file or another process is a
line changed in `arandu.mod.toml` in the same commit — and, for a file, a
conversation first, because this package names no path by design.

The `framework` line is a floor, and it is a second declaration of something
`go.mod` already says. Move both together when you bump.

## Adding a dependency

Do not, without an argument. This module goes into other people's builds: a
second `require` is a download and an audit surface for every project that
installs the package. What is here is the framework and hesape (for
`auth.Writable`), and the FAISS C API through cgo, which is not a Go
dependency at all:

```sh
export GOWORK=off
go list -m -f '{{if and (not .Indirect) (not .Main)}}{{.Path}} {{.Version}}{{end}}' all
```

## Moving FAISS

The C API functions the binding calls are listed in the preamble of
`engine_cgo.go`. A FAISS release that changes one of them, or the format
`write_index` produces, is a changelog line and, if stored indexes stop
reading, an `UPGRADE.md` section that tells installers to `Rebuild`. Run the
whole suite against the new version on both systems before moving the version
the README and CI name.

## Publishing

A published Go module version is **immutable**. The proxy serves it forever, and
a mistake is corrected by another release, never by moving a tag.

Before tagging:

- the gates pass with `GOWORK=off`, with cgo against FAISS and without cgo;
- `arandu.mod.toml` matches the code;
- `CHANGELOG.md` has the entry, under `## [x.y.z] - <date>`, in the Keep a
  Changelog sections — `Added`, `Changed`, `Fixed`, `Removed` — and
  `UPGRADE.md` has `## vx.y.z`; `TestEveryChangelogVersionHasUpgradeNotes`
  fails on one without the other.

While the version starts with `v0.`, breaking is allowed — freezing a shape this
early is worse. Breaking *quietly* is not. Say what it was, what it is, and what
they have to write instead. Run `apidiff` against the release baseline; the CI
step named `api diff against the last release` fails unless `UPGRADE.md` names
each removal.

Three changes break an installer without touching a signature, and each one is
worth a line in the changelog:

- **The envelope.** Stored indexes in every installer's bucket are read by it;
  a new layout reads the old one or says to `Rebuild`.
- **A default.** `DefaultFactory`, `MetricCosine` and `DefaultMaxIndexes` are
  what an application gets by leaving a field empty, and a changed factory or
  metric makes every stored index `ErrSpecMismatch`.
- **The meaning of `Score`.** Applications hold thresholds against it.

## Reporting a vulnerability

Not in an issue and not in a pull request. `SECURITY.md` has the private
advisory address. Anything that lets a search reach another tenant's vectors,
keeps vectors after their erasure returned, loads an index under another
specification, or lets an input crash the process through the binding is in
scope.
