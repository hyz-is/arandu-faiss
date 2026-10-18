<p align="center">
  <img src=".github/logo.svg" alt="Arandu" width="180">
</p>

# Arandu FAISS

It searches vectors with [FAISS](https://github.com/facebookresearch/faiss) and
answers with candidates, and it decides nothing.

It was built for 1:N duplicate detection of face embeddings in KUAA: a new
selfie's SFace template (128 `float32`, compared by cosine) is searched against
the templates already enrolled by the same tenant, and the answer is "these
enrolled templates are the nearest, with these scores". Whether any of them is
the same person is the application's decision, taken with its own thresholds and
recorded as its own evidence.

What it holds:

- **Index** — one FAISS index of one embedding model's vectors, each under an
  `int64` id the application chose. `Add`, `Remove`, `Search`, `Len`,
  `Serialize` and `Deserialize`. Exact cosine search by default (`Flat`, inner
  product over L2-normalized copies); HNSW and IVF through a factory string for
  scale. Safe for concurrent use: searches run in parallel, writes run alone.
- **Registry** — one index per **tenant and model**, reached only with the
  `security.Grant` the application's policy issued: the tenant is
  `data.Tenant(g)`, never a value from the request. Indexes are loaded lazily
  from a `Store` the application implements, every write is saved before it
  returns, and at most `MaxIndexes` stay in memory.
- **Provenance** — every search answers the engine and its version, the model,
  dimension, metric, factory string, parameters, whether the search was
  exhaustive or approximate, and how many vectors were searched.
- **Module** — a `foundation.Module` with no route and no table. `Boot` checks
  FAISS is linked and builds every configured model's index once; `Health` and
  `Close` follow.

## What it is not

- **Not a decision.** There is no threshold, no "match", no "duplicate", no
  boolean anywhere in what a search answers. `tests/Unit/decision_test.go`
  refuses those names and walks the result types for booleans.
- **Not a store.** It keeps no file and opens no connection. A serialized index
  is bytes handed to the application's `Store`, which seals them and puts them
  where the application keeps files (in KUAA, a private Cloudflare R2 bucket).
- **Not the source of truth.** The application keeps its encrypted templates;
  an index is derived from them and can always be rebuilt from them
  (`Registry.Rebuild`).
- **Not a server.** Registering it adds no route, no table and no background
  loop.

## Requirements

| | |
| --- | --- |
| Go | 1.26 or later (`go.mod` declares the minimum) |
| cgo | `CGO_ENABLED=1` and a C compiler. The binding is this package's own, over FAISS's C API (`libfaiss_c`) |
| FAISS | built with its C API. Developed and tested against **1.15.1** |
| Arandu | framework 0.56 or later |

Built with `CGO_ENABLED=0`, or with `-tags nofaiss`, the package still
compiles: `Available()`, `NewIndex`, `Deserialize`, every `Registry` call and
the module's `Boot` and `Health` answer `ErrUnavailable`. A build without FAISS
never answers an empty list of candidates that would read as "nothing near".

### Installing FAISS

| OS | install | what the cgo directives expect |
| --- | --- | --- |
| macOS, Apple Silicon | `brew install faiss` | `/opt/homebrew/opt/faiss/{include,lib}` |
| macOS, Intel | `brew install faiss` | `/usr/local/opt/faiss/{include,lib}` |
| Linux | build from source with the C API, below | `/usr/local/{include,lib}` |
| Windows | not supported: the serialization uses `open_memstream` and `fmemopen` | |

Homebrew's formula builds the C API and links OpenBLAS and libomp. Most Linux
distribution packages do not ship `libfaiss_c`, so on Linux it is built:

```bash
sudo apt-get install -y cmake g++ libopenblas-dev
git clone --depth 1 --branch v1.15.1 https://github.com/facebookresearch/faiss.git
cmake -S faiss -B faiss/build -DCMAKE_BUILD_TYPE=Release \
  -DFAISS_ENABLE_GPU=OFF -DFAISS_ENABLE_PYTHON=OFF -DFAISS_ENABLE_C_API=ON \
  -DBUILD_SHARED_LIBS=ON -DBUILD_TESTING=OFF
cmake --build faiss/build -j"$(nproc)"
sudo cmake --install faiss/build   # libfaiss, libfaiss_c and the c_api headers
sudo ldconfig
```

FAISS somewhere else is named with the standard cgo variables, which add to the
directives in `engine_cgo.go`:

```bash
export CGO_CFLAGS="-I/opt/faiss/include"
export CGO_LDFLAGS="-L/opt/faiss/lib -Wl,-rpath,/opt/faiss/lib"
```

The binary links `libfaiss_c` dynamically, so the machine that runs it has the
FAISS shared libraries (and OpenBLAS and the OpenMP runtime they need) too.
FAISS parallelizes inside a search with OpenMP; `OMP_NUM_THREADS`, read by the
OpenMP runtime and not by this package, bounds it.

## Install

```bash
go get github.com/hyz-is/arandu-faiss@v0.1.0
```

## Wire it

An Arandu application registers a module explicitly. There is no service
provider, no container and no discovery, so these are the lines to paste into
`bootstrap/app.go` and there are no others.

The import, with the other module imports:

```go
import (
	faiss "github.com/hyz-is/arandu-faiss"
)
```

The construction, in `Build`, before `k.Register`, with every value from the
application's typed configuration (the package reads no environment variable)
and the application's own `Store`:

```go
	faissModule, err := faiss.New(faiss.Config{
		Models: []faiss.IndexSpec{
			{Model: "sface_2021dec", Dim: 128}, // cosine, exact: the defaults
		},
		MaxIndexes: cfg.FAISS.MaxIndexes, // 0: 64 indexes in memory
	}, faceIndexStore) // the application's Store: seals the bytes, keeps them in R2
	if err != nil {
		return App{}, err
	}
```

And the registration, inside the `k.Register(...)` call already there:

```go
		faissModule,
```

Then hand `faissModule.Registry()` to the service that screens faces. There is
no migration: the package owns no table.

## Use it

Every call takes the `security.Grant` the application's policy issued for the
use case, and the model id:

```go
g, err := security.Authorize(ctx, policy, actor, ScreenFace, record)
if err != nil {
	return err
}

// Enrol: the template the application has just encrypted and kept.
err = registry.Add(ctx, g, "sface_2021dec", []int64{template.Label}, [][]float32{embedding})

// Screen: the nearest enrolled templates of this tenant.
result, err := registry.Search(ctx, g, "sface_2021dec", embedding, 10)
for _, c := range result.Candidates {
	// c.ID is a template label, c.Score its cosine similarity.
	// The application applies its threshold, decides, and records
	// result.Provenance with the evidence.
}

// Erasure (LGPD): gone from memory and from the Store when it returns nil.
removed, err := registry.Remove(ctx, g, "sface_2021dec", []int64{template.Label})
```

A job with no request behind it uses `security.SystemGrant(action, tenant)`
with the tenant of the records it is processing; a Grant with no tenant is
refused with `ErrNoTenant`, and a read-only Grant (a platform administrator
viewing as a member) searches and cannot write (`ErrReadOnly`).

**Ids are the application's.** They are `int64` and zero or more (FAISS answers
`-1` for "no result"). The application keeps the mapping from its template
records to these labels — a per-tenant sequence column is enough — and that
mapping is what turns a candidate back into a record. An id is stored once;
replacing a template is `Remove`, then `Add`.

**Rebuild from the templates.** `Registry.Rebuild` replaces an index with
exactly the vectors given, and saves it before it replaces the old one. It is
how an index is first built from existing enrolments, how it comes back after a
model's configuration changed (a stored index is never loaded under another
specification: `ErrSpecMismatch`), and how an IVF index is trained on every
vector at once.

### The Store

```go
type Store interface {
	Load(ctx context.Context, key faiss.Key) ([]byte, error) // ErrNotStored when absent
	Save(ctx context.Context, key faiss.Key, blob []byte) error
	Delete(ctx context.Context, key faiss.Key) error
}
```

`Key` is `{Tenant, Model}`, built by the registry from the Grant. The bytes are
every vector of one tenant's index — biometric data — so the application seals
them before upload and keeps them in a private bucket under an object key it
derives server side, encoding `Tenant` and `Model` rather than writing them into
a path as they are. `Save` returns when the bytes are durable: a registry write
is done when `Save` returned nil, and when `Save` fails the registry discards
its copy in memory, so it never answers from a change the Store does not hold.
`MemoryStore` is a Store for tests and development.

The serialized form is FAISS's own format inside an envelope that records the
model, the dimension, the metric, the factory string, the vector count and the
SHA-256 of the FAISS bytes; `Deserialize` checks all of it before and after
FAISS reads. It is integrity, not secrecy: sealing is the application's.

## Configuration

| field | required | meaning |
| --- | --- | --- |
| `Models` | yes | the embedding models: `Model` id, `Dim`, and optionally `Metric`, `Factory`, `Parameters` |
| `Factory` | no | factory string of a model that leaves its own empty. Defaults to `Flat` |
| `Metric` | no | metric of a model that leaves its own empty. Defaults to `cosine` |
| `MaxIndexes` | no | how many indexes stay in memory. Defaults to 64; the least recently used one that nobody holds is closed past it |

There is no tenant to set: the tenant of every index is the one on the Grant of
each call.

### Metrics

| `Metric` | FAISS | `Candidate.Score` |
| --- | --- | --- |
| `cosine` (default) | inner product over L2-normalized copies | cosine similarity, -1 to 1, higher is closer |
| `inner_product` | inner product, no normalization | higher is closer |
| `l2` | squared L2 | lower is closer |

### Factory strings and recall

| `Factory` | search | `Remove` | notes |
| --- | --- | --- | --- |
| `Flat` (default) | exhaustive, exact | in place | the right choice up to some hundreds of thousands of vectors per tenant |
| `HNSW32` | approximate (graph) | rebuilds the index from its own vectors | `Parameters: "efSearch=128"` trades speed for recall; the default `efSearch` of 16 can miss true neighbours |
| `IVF1024,Flat` | approximate (inverted lists) | rebuilds the index from its own vectors | trained on the first batch, so built with `Rebuild` over a large, representative set; `Parameters: "nprobe=16"` sets how many lists are searched |

An approximate index may miss a true nearest neighbour, and for duplicate
detection a missed neighbour is a missed duplicate. Measure recall against
`Flat` on the application's own data before switching, and record
`Provenance.Method` with every result. Compressed encodings (`PQ`, `SQ`) are
accepted by FAISS, but `Remove` rebuilds them from lossy reconstructions; an
application using one rebuilds from its templates after an erasure.

## Concurrency

`Index` is safe for concurrent use: searches hold a read lock and run in
parallel, `Add`, `Remove` and `Close` hold the write lock. FAISS's C API keeps
its last error per thread, so each call locks the goroutine to its OS thread
until the error is read. `Registry` shares one load between concurrent callers
of one index, writes one index at a time with its Save, and never closes an
index under a caller. `go test -race ./...` exercises all of it.

## Layout

```
module.go        registration: Name, Routes (none), Boot, Health, Close, Registry
config.go        what the application passes in
spec.go          IndexSpec, metrics, factory strings, limits
index.go         Index, Candidate, Provenance
registry.go      Registry and Result: one index per tenant and model, write-through
store.go         Store, Key, MemoryStore
envelope.go      the serialized form around FAISS's own bytes
engine_cgo.go    the binding to FAISS's C API (cgo, not nofaiss)
engine_nocgo.go  the stub that answers ErrUnavailable
errors.go        every error, for errors.Is
```

## Tests

```bash
export GOWORK=off CGO_ENABLED=1
go test -race ./...
CGO_ENABLED=0 go test ./...
```

Every vector in the tests is synthetic, drawn from a seeded generator. The
exhaustive search is held to a float64 brute force under each metric; removal,
the serialization round trip, specification mismatch and corruption, tenant
isolation, refused grants, write-through and erasure across registries, failed
saves, eviction and concurrent tenants are each a test. The tests that need
FAISS build only with cgo; the build without it has its own.

## Licences

This package: MIT. See [LICENSE.md](LICENSE.md). Copyright HYZIS - SERVICOS
DIGITAIS LTDA - EPP.

FAISS: MIT, Copyright (c) Facebook, Inc. and its affiliates (Meta Platforms). It is linked,
not vendored; see [NOTICE](NOTICE) for what an application that distributes a
binary linking it carries.
