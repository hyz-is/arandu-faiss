---
name: faiss-package
description: Install, wire and use the Arandu FAISS package (Go, Arandu) in an application. Use when the request is to "install arandu-faiss", "add FAISS", "face duplicate detection", "1:N search", "search similar faces", "go get github.com/hyz-is/arandu-faiss", "wire it into bootstrap/app.go", "implement the Store", "save the index to R2", "the build fails with faiss/c_api/... not found", "library not loaded: libfaiss_c", "FAISS is not linked into this build", "ErrNoTenant", "ErrReadOnly", "ErrSpecMismatch", "unknown model", "erase a template", "rebuild the index", or when a project's go.mod already requires github.com/hyz-is/arandu-faiss. Covers the build requirement, the wiring and where it goes, the Config fields, the Grant every call takes, the Store contract, and what each error means.
license: MIT
metadata:
  audience: app
---

# Using Arandu FAISS

A module that keeps one FAISS index per tenant and embedding model and answers
a query with the nearest stored vectors: ids, scores and the provenance of the
search. It decides nothing — whether a candidate is the same person is the
application's decision. It is registered by hand: there is no service
provider, no container and no discovery.

## Build requirement

`CGO_ENABLED=1`, a C compiler, and FAISS built with its C API (`libfaiss_c`)
on the machine that builds and the one that runs. macOS: `brew install faiss`.
Linux: build FAISS 1.15.1 from source with `-DFAISS_ENABLE_C_API=ON
-DBUILD_SHARED_LIBS=ON` and install it to `/usr/local` (the README has the
commands); elsewhere, add `CGO_CFLAGS=-I<prefix>/include` and
`CGO_LDFLAGS="-L<prefix>/lib -Wl,-rpath,<prefix>/lib"`. Windows is not
supported.

Built with `CGO_ENABLED=0` the application still compiles, and every call,
`Boot` included, answers `ErrUnavailable`.

## Install

```bash
go get github.com/hyz-is/arandu-faiss@v0.1.0
```

## The wiring, and where it goes

All of it is in `bootstrap/app.go`.

```go
import (
	faiss "github.com/hyz-is/arandu-faiss"
)
```

In `Build`, before `k.Register`, with every value from the application's typed
configuration (the package reads no environment variable) and the
application's own `Store`:

```go
	faissModule, err := faiss.New(faiss.Config{
		Models: []faiss.IndexSpec{
			{Model: "sface_2021dec", Dim: 128}, // cosine and Flat (exact) by default
		},
		MaxIndexes: cfg.FAISS.MaxIndexes, // 0: 64
	}, faceIndexStore)
	if err != nil {
		return App{}, err
	}
```

Inside the `k.Register(...)` call:

```go
		faissModule,
```

Then hand `faissModule.Registry()` to the service that screens. `Boot` checks
FAISS is linked and builds each model's index once; shutdown frees them. There
is no migration: the package owns no table.

## Every call takes the Grant

```go
g, err := security.Authorize(ctx, policy, actor, ScreenFace, record)
result, err := registry.Search(ctx, g, "sface_2021dec", embedding, 10)
err = registry.Add(ctx, g, "sface_2021dec", []int64{label}, [][]float32{embedding})
removed, err := registry.Remove(ctx, g, "sface_2021dec", []int64{label})
err = registry.Rebuild(ctx, g, "sface_2021dec", labels, embeddings)
err = registry.Drop(ctx, g, "sface_2021dec")
```

The tenant is `data.Tenant(g)`. Never build a Grant for a tenant the request
named; a job uses `security.SystemGrant(action, tenant)` with the tenant of the
records it processes. Ids are `int64`, zero or more, and the application keeps
the mapping from its template records to them.

Apply the threshold in the application, decide there, and record
`result.Provenance` (engine version, model, factory, metric, method, size)
with the evidence. `Provenance.Method` is `approximate` for HNSW and IVF: say
so in the evidence.

## The Store

Implement `Load` (answer `faiss.ErrNotStored` when absent), `Save` (return only
when durable) and `Delete` (absent is not an error). The bytes are every vector
of one tenant's index — biometric data: seal them before upload, keep them in a
private bucket (R2), derive the object key server side from `Key`, encoding
`Tenant` and `Model`, and audit every access. `faiss.NewMemoryStore()` is for
tests only.

## Symptoms to recognise

- **`faiss/c_api/...: file not found` or `library 'faiss_c' not found` at
  build.** FAISS is not installed with its C API where the directives look;
  install it or set `CGO_CFLAGS`/`CGO_LDFLAGS`.
- **`Library not loaded: libfaiss_c` at start.** The running machine lacks the
  shared library, or the rpath does not reach it.
- **`FAISS is not linked into this build`** (`ErrUnavailable`). The binary was
  built with `CGO_ENABLED=0` or `-tags nofaiss`.
- **`ErrNoTenant`.** The Grant is the zero Grant or a `SystemGrant` with an
  empty tenant.
- **`ErrReadOnly`.** A platform administrator viewing as a member tried to
  write; searches still work.
- **`ErrUnknownModel`.** The model id is not in `Config.Models`.
- **`ErrDimension`.** A vector from another model, or a truncated one.
- **`ErrSpecMismatch` on a search.** The stored index was built under another
  model, dimension, metric or factory string; `Rebuild` it from the templates.
- **`ErrCorrupt`.** The Store answered bytes that are not the index it saved.
- **`ErrIDExists`.** The label is already indexed; replacing a template is
  `Remove`, then `Add`.
- **A save error from `Add` or `Remove`.** The Store refused; the registry
  discarded its copy and the next call reloads what the Store holds. Retry the
  write.
