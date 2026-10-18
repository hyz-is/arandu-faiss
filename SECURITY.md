# Security policy

## Supported versions

The latest minor release of Arandu FAISS receives security fixes. Older minor
releases do not: a published Go module version is immutable, so a fix is a new
release rather than a moved tag.

## Reporting a vulnerability

Report it privately, through GitHub's advisory form:

<https://github.com/hyz-is/arandu-faiss/security/advisories/new>

Do not open a public issue and do not describe the problem in a pull request.
A report that arrives in public is a report every reader of this repository can
act on before there is a release to upgrade to.

Include what you need to reproduce it: the version, the wiring, and the request
or call that triggers it.

Expect an acknowledgement within a few days. If the report is confirmed, the
fix, the release and the advisory are published together, and you are credited
unless you ask not to be.

## What is in scope

Anything that lets a caller reach vectors of a tenant its Grant does not name,
or keeps vectors somebody asked to erase. In particular:

- a `Registry` path whose tenant comes from anywhere but `data.Tenant(g)`, or a
  search that answers another tenant's ids;
- a write accepted with a Grant that carries no tenant, or with a read-only
  Grant;
- a `Remove` or `Drop` that returns nil while the vectors are still in memory
  or in the Store, or an index answered from after its Save failed;
- a serialized index loaded under another model, dimension, metric or factory
  string, or altered bytes reaching FAISS's reader;
- an input -- a vector, a blob, a configuration -- that crashes the process or
  corrupts memory through the binding;
- a result type that carries a decision.

## What is not

- A vulnerability in FAISS itself. Report that to FAISS, and here too if this
  package makes it reachable.
- What an application's Store does with the bytes. Sealing them and keeping
  them private is the application's, as the README says.
