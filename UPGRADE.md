# Upgrade Guide

Every release that changes what an installer has to write is described here,
newest first. A release with nothing to change still has a heading, so this file
and [CHANGELOG.md](CHANGELOG.md) describe the same set of versions.

## v0.1.0

The first release, proposed as `v0.1.0`. Nothing to upgrade from.

A serialized index carries its own envelope version. A later release that
changes the envelope or the meaning of a specification says here whether
stored indexes are read as they are or rebuilt from the application's
templates with `Registry.Rebuild`.
