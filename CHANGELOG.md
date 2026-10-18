# Changelog

Everything worth knowing about a release of Arandu FAISS is recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
the versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

A published module version is immutable: Go serves it from the proxy forever, so
a release is corrected by another release and never by moving a tag.

## [Unreleased]

Proposed as `v0.1.0`, the first public release, as
`github.com/hyz-is/arandu-faiss`. When it is tagged, this section moves under
`## [0.1.0] - <date>` and `UPGRADE.md` gains `## v0.1.0`.

### Added

- `Index`: one FAISS index of one embedding model's vectors, under `int64` ids
  the caller chooses, built by the index factory as IDMap2 around the
  configured structure. `Add` checks a whole batch before FAISS sees it;
  `Search` answers up to `k` `Candidate`s, closest first, with FAISS's score;
  `Remove` is idempotent for absent ids, in place for `Flat` and by rebuilding
  from the stored vectors for every other structure; `Len`, `Dim`, `Model`,
  `Spec`, `Contains`, `Provenance` and `Close`. Safe for concurrent use.
- `IndexSpec`: model id, dimension, metric (`cosine` by default, as inner
  product over L2-normalized copies; `inner_product`; `l2`), factory string
  (`Flat` by default, exact) and FAISS search parameters (`efSearch`,
  `nprobe`), validated before FAISS is called.
- `Serialize` and `Deserialize`: FAISS's own format, written in memory, inside
  an envelope recording model, dimension, metric, factory string, count and
  the SHA-256 of the FAISS bytes. Another specification is refused with
  `ErrSpecMismatch`, altered bytes with `ErrCorrupt`.
- `Registry`: one index per tenant and model, reached with the
  `security.Grant` the application's policy issued (tenant from
  `data.Tenant(g)`, `ErrNoTenant` without one, `ErrReadOnly` for an
  impersonated subject's writes). Lazy load from the application's `Store`,
  one load shared by concurrent callers, write-through saves, `Rebuild`,
  `Drop`, and at most `MaxIndexes` loaded. `Result` carries the candidates and
  their `Provenance`.
- `Store`, `Key` and `MemoryStore`.
- `Module`: `foundation.Module` with no route and no table, plus `Bootable`
  (checks FAISS and builds every configured model's index), `Health` and
  `Closable`. `Config`: `Models`, `Factory`, `Metric`, `MaxIndexes`.
- A build without cgo, or with `-tags nofaiss`, compiles and answers
  `ErrUnavailable` from every operation.
- Tests over seeded synthetic vectors only, a capability and tenant audit, and
  a decision test that refuses verdict names and booleans in search results.

### Requirements

- Go 1.26, `CGO_ENABLED=1`, a C compiler, and FAISS built with its C API
  (`libfaiss_c`). Developed and tested against FAISS 1.15.1 (Homebrew, macOS
  arm64). Framework `v0.56.0`.
