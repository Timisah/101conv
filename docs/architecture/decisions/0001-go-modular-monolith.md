# ADR 0001: Rebuild the backend as a Go modular monolith

- **Status:** Accepted
- **Date:** 2026-10-16

## Context

101conv needs one installable service that serves a browser UI and API, accepts durable conversion jobs, and runs profile-selected converter processes under strict limits. Its first deployment should be straightforward for a self-hosted Docker operator. The existing Spring application demonstrates behavior but does not constrain the replacement.

## Decision

Build the replacement in Go as one modular codebase with Core and Worker runtime roles. Use PostgreSQL for durable jobs and metadata, managed filesystem storage for files, and versioned REST contracts for both the browser client and external clients. Keep converter execution behind typed adapters and a separately constrained Worker container. Use the standard library where it keeps the service small; add dependencies only for concrete needs such as PostgreSQL access.

Preconfigure the version-aware 101conv Official Catalog on every clean install. A fresh instance has no installed third-party converters. Catalog and profile app-version ranges determine which entries the current release may install; package download and installation remain explicit administrator actions.

## Rationale

Go builds a small static service binary and offers direct support for HTTP servers, process deadlines, cancellation, and concurrency. That suits a small self-hosted Compose deployment and keeps runtime packaging simpler than the current JVM service. C could produce a small binary too, but the additional implementation and memory-management burden does not help this product's dominant work: API, persistence, catalog, and worker orchestration.

## Consequences

- The Go modular monolith is the only supported application implementation. Compatibility is defined by API contracts and current Go data formats, not by retaining obsolete runtime code.
- PostgreSQL and a Worker role add operational components, but provide restart recovery and isolate converter execution from HTTP request handling.
- Schema migration preserves existing Go accounts, histories, configuration, installed profiles, and user files. Compose updates do not remove persistent directories or named volumes.
- The built-in catalog can be present while empty. Official profiles will be published only after package verification, platform compatibility, and licensing metadata are established.
