# Real PLC qualification

The test in this directory is excluded from normal `go test ./...` runs. It
uses the `s7integration` build tag, an external configuration, and read-only
behavior by default.

本目录中的测试不会进入普通 `go test ./...`。它使用 `s7integration` build tag、
仓库外配置，并且默认只读。

1. Copy `config.example.json` outside the repository and replace its
   documentation addresses with dedicated, authorized PLC addresses.
2. Set the absolute configuration path and run the complete qualification
   test so that the read gate cannot be skipped.

```powershell
$env:GOS7_INTEGRATION_CONFIG = 'D:\private\gos7-integration.json'
go test -tags=s7integration -run TestS7HardwareQualification -v ./test/qualification
```

For explicit echo-write qualification, add fields like these to the same
external JSON object:

```json
{
  "echoWrites": [
    {"name": "dedicated-test-bit", "address": "DB1.DBX200.0", "transport": "bit"}
  ],
  "writeAllowlist": ["DB1.DBX200.0/bit/1"]
}
```

Then provide the exact second consent gate and run the complete test again:

```powershell
$env:GOS7_INTEGRATION_WRITE = 'ECHO-WRITE-TO-ALLOWLIST'
go test -tags=s7integration -run TestS7HardwareQualification -v ./test/qualification
```

`echoWrites` first reads the target, writes the same raw bytes to the exact
allowlisted target, and reads it again. Writes do not start after a failed read
qualification, and later writes stop after the first failed echo write. Payload
bytes are never printed.

`exchangeTimeoutMillis` limits each PLC request/response exchange. The
qualification harness separately derives a bounded context for the complete
logical operation, so fragmented reads do not share one exchange deadline.

`echoWrites` 会先读取目标，将完全相同的原始字节写回精确匹配的白名单目标，再次
回读。只读资格测试失败后不会开始写入，第一次 echo write 失败后不会继续后续
写入，且测试永远不会打印 payload bytes。
