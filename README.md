# gos7

[简体中文](docs/i18n/zh-CN/README.md) · [Implementation specification](docs/spec.md) ·
[Documentation index](docs/README.md)

`gos7` is a pure-Go client for classic Siemens S7comm over ISO-on-TCP
(RFC 1006). It provides a small, context-aware API for absolute DB, input,
output, marker, timer, and counter access.

This is an intentionally incompatible redesign of `robinson/gos7`. It keeps
the BSD-3-Clause license and upstream attribution while removing the legacy
Handler, AG/PG, CPU-control, NCK, MPI, and PPI APIs.

> **Status:** `v0.1.0` is the first source release of the redesigned API.
> Real-controller qualification is deferred and no hardware model is claimed as
> certified. The API may change before v1.0.0.

## Platform support

- Go 1.20 or newer at source level; CI tests Go 1.20.14 and current stable;
- no CGO and no native library;
- Windows 7 amd64 build target when the application is built with Go 1.20.x;
- Windows amd64/arm64 and Linux amd64/arm64/ARMv7.

Go 1.20.14 is retained as a legacy build line because Go 1.20 is the final Go
line that can produce Windows 7-compatible binaries. It no longer receives Go
security fixes, so current stable Go is the primary test, race, fuzz, and
vulnerability-scan runtime. Building with a newer Go toolchain does **not**
preserve Windows 7 runtime compatibility. Windows 7 runtime qualification is
deferred, and Windows 7 ARM64 is not supported.

## Implemented scope

- TCP, TPKT, COTP, Setup Communication, ReadVar, and WriteVar;
- rack/slot or explicit TSAP addressing, IPv4/IPv6, local IP binding,
  keepalive, connection/per-exchange deadlines, and context cancellation;
- negotiated TPDU and S7 PDU limits, 240..960-byte requested PDUs, a 20-item
  per-PDU compatibility limit, validated batch packing, large-read reassembly,
  and explicit continuous-area chunking;
- input-aligned per-item results, typed errors, and explicit write outcomes;
- native bit writes without hidden read-modify-write;
- absolute address parser/formatter and bounds-checked scalar, REAL/LREAL,
  classic STRING, Counter, and S5TIME codecs;
- local diagnostics that do not generate PLC traffic.

One `Client` owns one physical S7 session. Concurrent callers are safe, while
complete logical operations are serialized on that session. `Close` is
idempotent and terminal.

The library does **not** implement retry, automatic reconnect, polling,
pooling, tag cache, deadband, address coalescing, discovery, CPU control, block
management, password operations, or NCK access. Those policies belong to the
application or driver runtime.

## Install

```text
go get github.com/punk-one/gos7@v0.1.0
```

## Quick use

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

client, err := gos7.Dial(ctx, gos7.Config{
	Endpoint: "192.0.2.10:102",
	Addressing: gos7.Addressing{
		Mode: gos7.AddressByRackSlot,
		Rack: 0,
		Slot: 2,
		Role: gos7.ConnectionPG,
	},
})
if err != nil {
	return err
}
defer client.Close()

address, _, err := gos7.ParseAddress("DB1.DBD4")
if err != nil {
	return err
}
results, batchErr := client.Read(ctx, []gos7.ReadItem{{
	Address: address, Transport: gos7.TransportReal, Count: 1,
}})
if len(results) != 1 || results[0].Err != nil {
	return fmt.Errorf("read failed: batch=%v results=%v", batchErr, results)
}
value, err := codec.Float32(results[0].Data)
```

Complete runnable programs:

- [batch REAL and BOOL read](examples/batch-read/main.go);
- [guarded WORD write with explicit outcome handling](examples/safe-write/main.go);
- [commands and safety notes](examples/README.md).

`New` validates configuration without network I/O. `Dial` performs `New` and
`Connect`. Endpoints without a port use TCP 102. `ExchangeTimeout` bounds each
individual request/response exchange; the caller's context bounds the complete
logical operation, including queueing and all fragments.

## Addresses and data

| Address examples | Meaning |
| --- | --- |
| `DB1.DBX0.7`, `DB1.DBB2`, `DB1.DBW4`, `DB1.DBD8` | DB bit/byte/word/dword |
| `I0.0`, `IB2`, `IW4`, `ID8` | input (`E` alias accepted) |
| `Q0.0`, `QB2`, `QW4`, `QD8` | output (`A` alias accepted) |
| `M0.0`, `MB2`, `MW4`, `MD8` | marker |
| `T12`, `C7` | timer and counter |

Address width is a location hint, not a semantic type. `DB1.DBD4` can start a
DWORD, DINT, REAL, or an eight-byte LREAL. LREAL uses `TransportByte` with
count 8. A classic STRING uses `TransportByte` with count `2 + capacity`.
Character encoding, scale, unit, quality, and timestamp remain application
policy.

## Result and reconnect rules

`Read` and `Write` results remain aligned with input order. Always inspect the
batch error and every item result; one PLC rejection does not hide successful
siblings.

| Write outcome | Meaning |
| --- | --- |
| `WriteNotAttempted` | no request was issued for the item |
| `WriteAcknowledged` | the PLC returned a matching success response |
| `WriteRejected` | the PLC returned a definite rejection |
| `WriteUnknown` | the request may have been applied, but no trusted response arrived |

Never automatically retry `WriteUnknown`; reconcile the PLC/process state
first. Transport, timeout, or protocol errors can mark a session broken. The
application may call `Connect` again on a non-closed client after its own policy
allows it. The library never reconnects implicitly.

`ReadArea`/`WriteArea` are for explicitly chunkable DB/I/Q/M byte ranges. On
failure, their returned `n` is only the confirmed contiguous prefix. Prefer
ordinary `Write` for one typed value.

## Repository layout

```text
gos7/                       public session, item, error, and diagnostic API
codec/                      public bounds-checked Siemens value codecs
internal/isotcp/            bounded TPKT/COTP framing
internal/protocol/          S7comm request/response encoding
internal/testplc/           offline scripted PLC used only by tests
docs/                       public documentation and translations
examples/                   runnable public-API examples
test/                       public-API black-box tests
test/qualification/         opt-in real PLC qualification
```

A root black-box lifecycle test keeps ordinary root-package tooling active.
The larger public API suite, address fuzz target, and end-to-end benchmarks
live under `test/`; low-level ISO-on-TCP, protocol, and codec tests remain
beside their own subpackage implementation.

## Test and qualification

Default tests are pure Go and offline:

```powershell
$env:GOWORK = 'off'
$env:CGO_ENABLED = '0'
go test ./...
go vet ./...

# Optional Windows 7 legacy-toolchain verification from a modern Go command.
$env:GOTOOLCHAIN = 'go1.20.14'
go test ./...
```

Real PLC tests are read-only by default, require the `s7integration` tag, and
refuse repository-local configuration. See the
[qualification guide](test/qualification/README.md). Write qualification has
both an exact consent value and an exact target allowlist.

## Controller scope and safety

The v0.1 design targets S7-300/400 classic absolute access, S7-1200/1500 when
PUT/GET and standard non-optimized DB access are enabled, and S7-200 SMART
Ethernet after qualification against the exact CPU configuration. These are
targets, not a claim that every model has passed hardware testing.

S7comm Plus, symbolic/optimized access, protected TLS sessions, S7 routing,
MPI, and PPI are outside this version. Classic S7comm has no modern transport
security. Use it only on an authorized, segmented OT network with minimum PLC
write permissions.

## License

BSD-3-Clause. See [LICENSE](LICENSE), [NOTICE](NOTICE), and
[CHANGELOG.md](CHANGELOG.md). Siemens, SIMATIC, and S7 names are trademarks of
Siemens AG. This independent project is not affiliated with or endorsed by
Siemens.
