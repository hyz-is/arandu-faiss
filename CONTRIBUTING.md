# Contributing

Thank you for considering a contribution.

## Before you open a pull request

Run the gates. They are what CI runs, and a pull request that fails one of
them fails for everybody who pulls after it.

```bash
export GOWORK=off CGO_ENABLED=1
gofmt -l $(find . -name '*.go' -not -path '*/testdata/*' -not -name '*.kyse.go')
go build ./...
CGO_ENABLED=0 go build ./...
go vet ./...
go test -race -count=1 ./...
CGO_ENABLED=0 go test -count=1 ./...
```

FAISS has to be installed with its C API for the cgo lines; the README says
how on each system. The two filters on `gofmt` are not preference. `gofmt` is
the only tool in the chain that ignores build tags, so it reads files the
compiler deliberately excludes; `testdata/` holds fixtures that are invalid on
purpose.

## What a change has to keep true

Five properties are the reason this package is what it is. A change that breaks
one of them will not be merged, whatever else it improves.

1. **Nothing here decides.** A search answers candidates and provenance; a
   threshold, a verdict or a boolean in a result belongs to the application.
2. **The tenant comes from `data.Tenant(g)`.** Every `Registry` method takes a
   `security.Grant`, and no tenant arrives any other way.
3. **The package names no file.** Indexes are bytes handed to the
   application's `Store`.
4. **A write is in the Store when it returns,** and a failed save is never
   answered from.
5. **`arandu.mod.toml` matches the code,** in the build with cgo and in the
   one without.

A rule an index keeps is written once, in `index.go`, over the `engine*`
functions; the two engine files only implement or refuse them. A new call into
FAISS goes through `call`, which keeps the thread locked until FAISS's error is
read.

Every vector in a test is synthetic, from a seeded generator. No real face,
template or document is ever a fixture.

## Style

Everything in the source is in English: identifiers, doc comments, internal
comments, error messages, log messages, and the names and messages of tests.
`pkg.go.dev` publishes the doc comments, and its readers are users of this
package.

Every exported symbol carries a doc comment saying what it does. The comment
documents the symbol and nothing else: no dates, no issue numbers, no
references to other repositories. Why a signature is what it is belongs there
when it is a fact about the code; anything else belongs in the pull request.

## Commits

One commit per logical change. Formatting is not mixed with behaviour, because
a diff that does both is a diff nobody reviews.

## Reporting a vulnerability

Do not open an issue. See [SECURITY.md](SECURITY.md).

## Licence

By contributing you agree that your contribution is licensed under the MIT
licence, the same as the rest of github.com/hyz-is/arandu-faiss.
