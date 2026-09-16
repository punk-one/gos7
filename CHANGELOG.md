# Changelog

This project follows semantic versioning after `v1.0.0`. The `v0.x` API may
change while protocol and hardware evidence accumulates.

## v0.1.1 — 2026-09-16

### Fixed

- Accept COTP connection-confirm TSAP parameters in either the ISO-standard
  reversed order or the legacy request-echo order observed on some PLCs and
  communication processors. Both values must still match the requested pair;
  unrelated and partially mismatched TSAP responses remain rejected.

## v0.1.0 — 2026-08-21

First release of the redesigned `github.com/punk-one/gos7` module.

### Added

- Context-aware, concurrency-safe single-session `Client` for classic S7comm
  over ISO-on-TCP.
- Rack/slot and explicit TSAP addressing, IPv4/IPv6 endpoints, local binding,
  connection/per-exchange deadlines, and negotiated TPDU/PDU diagnostics.
- Validated ReadVar/WriteVar batching, large-read fragmentation, continuous
  `ReadArea`/`WriteArea`, native bit operations, and explicit write outcomes.
- A 20-item compatibility ceiling per wire PDU, a configurable logical
  fragment ceiling, and streaming read/write batch planning.
- Finite absolute address parser and formatter for DB/I/E/Q/A/M/T/C forms.
- Bounds-checked Bool, integer, REAL/LREAL, classic STRING, Counter, and S5TIME
  codecs.
- Pure-Go scripted PLC tests, protocol golden tests, fuzz targets, and an
  opt-in real-hardware qualification harness.
- Go 1.20 source baseline and a Windows 7 amd64 build target for binaries built
  with Go 1.20.x; runtime qualification is deferred.
- Windows amd64/arm64 and Linux amd64/arm64/ARMv7 no-CGO build matrix.
- Runnable batch-read and guarded-write examples plus isolated public-API and
  real-PLC qualification test directories.

### Changed

- Replaced the previous handler-centric API with small typed session and data
  plane APIs. This release is intentionally not source-compatible with the
  original `robinson/gos7` API.
- Changed timeout semantics so `ExchangeTimeout` bounds one request/response;
  the caller context bounds the complete multi-PDU operation.
- Restricted requested S7 PDU sizes to the commonly supported 240..960 range
  and bound every data frame by the negotiated COTP TPDU.
- Kept Go 1.20.14 as a Windows 7 legacy build line while using current stable
  Go for race, fuzz, coverage, and vulnerability checks.
- Moved localized project introductions under `docs/i18n/<locale>/` so the
  repository root retains one canonical README as languages are added.

### Removed

- AG/PG helper surface, CPU control, block/directory/security operations, NCK,
  MPI/PPI clients, global connection sharing, and bundled third-party binary
  documentation/assets.
- Implicit reconnect, read/write retry, controller probing, payload logging,
  and native-library dependencies.
- The lifecycle-only `Logger`/`WithLogger` option and unused unsupported-error
  category; logging and telemetry belong to the application layer.

### Release scope

The v0.1.0 source release is based on the offline protocol/public-API suite,
cross-build matrix, and documented security review. Real-controller and
Windows 7 runtime qualification are deferred; this release does not certify a
specific PLC, firmware, communication processor, or Windows 7 installation.
