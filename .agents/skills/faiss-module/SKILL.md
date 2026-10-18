---
name: faiss-module
description: Change this Arandu package's index, registry, store contract, envelope, FAISS binding, configuration or module. Use when the request is to "add a metric", "support another factory string", "add IVF", "tune HNSW", "add a threshold", "return whether it is a duplicate", "take the tenant as a parameter", "save the index to a file", "save less often", "batch the saves", "add a route", "string ids", "support Windows", "call another FAISS function", "the error message is wrong or empty", or when a change touches index.go, registry.go, store.go, envelope.go, spec.go, config.go, module.go, engine_cgo.go or engine_nocgo.go. Covers what may and may not change, and the tests that hold each property.
license: MIT
---

# Changing the engine

## Refused outright

- **A decision.** No threshold, `IsDuplicate`, `Match`, "same person" or
  boolean in a result. A search answers `Candidate{ID, Score}` and
  `Provenance`. `tests/Unit/decision_test.go` refuses the names and walks
  `Result`, `Candidate` and `Provenance` for booleans.
- **A tenant from anywhere but the Grant.** Every exported `Registry` method
  takes `ctx`, then a `security.Grant`, and builds its `Key` in `keyFor` from
  `data.Tenant(g)`. `TestTheTenantComesOnlyFromTheGrant` reads the syntax and
  fails on a `tenant` parameter or a `Key` whose `Tenant` came from elsewhere.
- **A file.** No `faiss_write_index_fname`, no path in `Config`, no `os.*`
  call. Bytes go to the `Store`. The capability audit fails on a file call
  because `arandu.mod.toml` says `filesystem = false`.
- **A route.** Search is reached through the application's controllers.
- **Python,** anywhere.
- **An environment variable** read by the package. Settings are `Config`
  fields.

## Where a rule goes

`index.go` holds every rule an index keeps — validation, the id set, locking,
the removal strategy, the provenance — once, over the `engine*` functions.
`engine_cgo.go` implements them over FAISS's C API and `engine_nocgo.go`
answers `ErrUnavailable`. A new engine function is added to both files with
the same signature, or one build stops compiling; `CGO_ENABLED=0 go build ./...`
is the gate that says so.

## Calling FAISS

Every C call goes through `call(op, fn)`. FAISS's C API keeps the last error
per thread, and `call` locks the goroutine's OS thread from the call to
`faiss_get_last_error`; a call outside it can read another call's message, or
none. A helper that needs several C calls in a row (a selector, a stream, a
parameter space) is a `static` function in the preamble, so it is one cgo call.

Pass Go memory only for the duration of the call, and never memory that holds
Go pointers. Free what FAISS allocates (`faiss_Index_free`, the memstream
buffer) on every path.

## Ids and removal

Every index is FAISS's `IDMap2` around the structure the factory string names,
so ids are the caller's. `Remove` removes in place only for `Flat`: IDMap2
compacts its id map on the assumption that the index below renumbers, which
`IndexFlat` does and IVF does not, so an in-place removal from IVF would
silently answer wrong ids. Every other structure is rebuilt from its stored
vectors; IVF keeps a direct map so it can read them back. A new structure that
can remove in place correctly is a measured change with a test in
`TestRemovedVectorsAreNeverAnsweredAgain`, not an assumption.

## Metrics and factory strings

A metric is a constant in `spec.go`, a case in `Validate`, a mapping in
`metricType`, the normalization rule in `appendVector`, a row in the README
table, and the brute force in `tests/Feature/helpers_test.go`. A factory
string needs nothing but a test: what FAISS refuses arrives as `ErrEngine` from
`NewIndex` and stops `Boot`.

## The envelope

`envelope.go` is the stored format. Its magic carries a version byte; a change
to the layout is a new version that still reads the old one, or an
`UPGRADE.md` section telling installers to `Rebuild`. Every field the envelope
records is checked on load, before and after FAISS reads.

## The registry

Writes take the key's stripe lock, run the change, serialize, and `Save`
before returning; a failed change or save calls `discard`, so the next call
reloads what the Store holds. An `entry`'s `idx` and `err` are written under
the registry's mutex before `ready` is closed; `loaded`, `refs`, `used` and
`evicted` are read and written only under it. An index is closed only when
nobody holds it. Any change here runs `go test -race -count=3 ./tests/Feature/`.

## Concurrency

`Index.Search` holds the read lock; `Add`, `Remove`, `Close` the write lock.
FAISS's CPU search is safe from several threads at once; its writes are not,
which is why they hold the write lock.
