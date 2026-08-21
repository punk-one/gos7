# gos7 examples

These programs use only the public `gos7` API and compile with Go 1.20.

## Batch read / 批量读取

The batch-read example reads one REAL and one BOOL while preserving per-item
errors. Replace the endpoint and addresses with values authorized for your PLC.

批量读取示例会读取一个 REAL 和一个 BOOL，并分别处理每个点的错误。请把 endpoint
和地址替换为已经授权的 PLC 配置。

```powershell
go run ./examples/batch-read `
  -endpoint 192.0.2.10:102 `
  -rack 0 -slot 2 `
  -real DB1.DBD4 `
  -bit DB1.DBX8.0
```

## Safe write / 受控写入

The safe-write example writes one signed 16-bit WORD. It refuses to write
unless the exact consent text is supplied. Use a dedicated test address and
read back or otherwise reconcile an unknown outcome before any retry.

受控写入示例只写一个 16 位有符号 WORD；未提供完全匹配的确认文本时会拒绝写入。
只能使用专用测试地址。写入结果为 unknown 时，任何重试前都必须先回读或按工艺
状态进行核对。

```powershell
go run ./examples/safe-write `
  -endpoint 192.0.2.10:102 `
  -rack 0 -slot 2 `
  -address DB1.DBW200 `
  -value 42 `
  -confirm WRITE-TO-DEDICATED-ADDRESS
```

Classic S7comm has no modern transport security. Run examples only on an
authorized, segmented OT network. / 经典 S7comm 不具备现代传输安全能力，只能在
经过授权且完成网络分区的 OT 网络中运行这些示例。
