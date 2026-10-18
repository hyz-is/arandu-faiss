# Working on Arandu FAISS

This is an Arandu package that searches vectors with FAISS and answers with
candidates. It keeps one index per tenant and embedding model, persisted
through a `Store` the application passes in, and it was built for KUAA's 1:N
duplicate detection of face embeddings (SFace, 128 `float32`, cosine).
It is a Go module somebody `go get`s and registers by hand in their own
`bootstrap/app.go`, which is the whole difference from working in an
application. There is no service provider, no container and no discovery — if a
line of wiring is not written in the installer's repository, it does not happen.

Read `.agents/skills/` before writing code. Each skill is a procedure, and the
one you need is named by the situation you are in.

## The rules

These come from the engines' design, not from taste. They were written for
KUAA, the identity-verification application this engine was built for, and
they hold for every installer. A change that breaks one is not merged.

1. **No decisions in an engine, and none here.** A search answers ids, scores
   and provenance. Never a threshold applied, a "match", a "duplicate", a
   "same person" or a boolean in a result. The application decides, records the
   evidence, and owns the threshold. `tests/Unit/decision_test.go` holds it.
2. **The tenant comes from the Grant.** Every `Registry` method takes the
   `security.Grant` the application's policy issued, and the tenant is
   `data.Tenant(g)`. No method takes a tenant, nothing reads one out of a
   request, and tenants never share an index. `tests/Unit/audit_test.go` holds
   it.
3. **The package names no file.** An index is serialized to bytes in memory and
   handed to the application's `Store`, which seals it and keeps it (in KUAA, a
   private Cloudflare R2 bucket). `arandu.mod.toml` says `filesystem = false`,
   and the audit fails the day that stops being true.
4. **Every write is in the Store when it returns.** `Add`, `Remove`, `Rebuild`
   and `Drop` save before returning, and a failed save discards the index in
   memory. `Remove` is the LGPD erasure path: when it returns nil the vectors
   are gone from memory and from the Store.
5. **No Python.** Not as a runtime, not in a gate, not in a test, not to
   generate a fixture. Tests use seeded synthetic vectors.
6. **Configuration is typed.** `Config` comes from the application. This
   package reads no environment variable.

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

`CGO_ENABLED=1` is required for the real build: the binding is cgo over FAISS's
C API, and FAISS has to be installed (`brew install faiss` on macOS; built from
source with `-DFAISS_ENABLE_C_API=ON` on Linux; see the README). The
`CGO_ENABLED=0` lines hold the stub: the package compiles without FAISS and
every operation answers `ErrUnavailable`. The tests that need FAISS are behind
the `cgo && !nofaiss` build constraint and do not skip: a run without FAISS runs
the stub's tests instead, and says so by which tests ran.

`GOWORK=off` matters because this checkout may sit beside a Go workspace that
does not list it; with the workspace on, every command above fails before it
compiles anything.

`aru doctor` reads applications, not libraries. Run here it exits 1 with
"this is not an Arandu project", and it does not read an installed package
either. That is why `tests/Unit/audit_test.go` compares `arandu.mod.toml` with
what the code calls, in both builds.

## What this repository holds

```
module.go        registration: Name, Routes (none), Boot, Health, Close, Registry
config.go        what the application passes in
spec.go          IndexSpec, metrics, factory strings, limits
index.go         Index, Candidate, Provenance
registry.go      Registry and Result: one index per tenant and model, write-through
store.go         Store, Key, MemoryStore
envelope.go      the serialized form around FAISS's own bytes
engine_cgo.go    the binding to FAISS's C API (cgo && !nofaiss)
engine_nocgo.go  the stub that answers ErrUnavailable (!cgo || nofaiss)
errors.go        every error, for errors.Is
```

`index.go` holds every rule an index keeps, once, over a handful of
`engine*` functions; `engine_cgo.go` implements them with FAISS and
`engine_nocgo.go` refuses them. A rule written into one engine file is a rule
the other build does not keep.

Tests live under `tests/Unit` (specification, configuration, the audit, the
decision rule, the release files) and `tests/Feature` (the index, the registry,
the module, concurrency, and the build without FAISS). The envelope is tested
beside the code, in `envelope_internal_test.go`, because it is unexported and
pure Go.

## What does not exist here, on purpose

| A model reaches for | What is here instead |
| --- | --- |
| a threshold, `IsDuplicate`, a `Match` boolean | `Candidate{ID, Score}` and `Provenance`. The application decides |
| a `tenant string` parameter | the `security.Grant`, and `data.Tenant(g)` |
| `faiss_write_index_fname`, a path in `Config` | `Serialize` to bytes, and the application's `Store` |
| a Go binding from elsewhere | `engine_cgo.go`, over FAISS's C API, with the thread locked around each call and its error |
| an index loaded under a changed `Config` | `ErrSpecMismatch`, and `Registry.Rebuild` from the application's templates |
| `IDMap` removal over IVF | a rebuild from the stored vectors: IDMap2 renumbers its ids on the assumption that the index below does, and IVF does not |
| a route that searches | nothing. Search is reached through the application's controllers, behind its policy |
| a table of ids | the application's own mapping from its templates to the `int64` labels |

## The binding

FAISS's C API reports failure as a return code and keeps the message per
thread (`faiss_get_last_error`). A goroutine may move between OS threads from
one cgo call to the next, so `call` in `engine_cgo.go` locks the thread from the
FAISS call to the read of its error. Every new C call goes through `call`.

The `#cgo` directives name Homebrew's paths on macOS and `/usr/local` on
Linux; an installer with FAISS elsewhere adds `CGO_CFLAGS` and `CGO_LDFLAGS`.
Serialization uses `open_memstream` and `fmemopen`, so Windows is not
supported.

## Writing code

Everything in the source is in English: identifiers, doc comments, internal
comments, error messages, log messages, and the names and messages of tests.

Every exported symbol carries a doc comment, and the comment documents the
symbol and nothing else. A date, an issue number, a version in progress or the
name of another repository does not belong there.

Tests go under `tests/`, in a capitalized category directory declaring a
lowercase external package: `tests/Unit` holds `package unit_test`,
`tests/Feature` holds `package feature_test`. Both import the package by its
module path, which is what makes them see exactly what a caller sees. A test
that genuinely needs something unexported goes beside the code as
`*_internal_test.go`, and the suffix is how it says so.

Agent instructions live only in this file and in `.agents/skills/`; there is no
instruction file named after a vendor.
