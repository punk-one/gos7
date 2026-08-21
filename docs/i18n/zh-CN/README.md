# gos7

[English source](../../../README.md) · [文档索引](../../README.md)

> 翻译语言：`zh-CN`；同步基线：`v0.1.0`，2026-08-21。英文根
> `README.md` 是功能和兼容性声明的规范来源。

`gos7` 是经典 Siemens S7comm over ISO-on-TCP（RFC 1006）的纯 Go 客户端，
提供小而明确、支持 `context.Context` 的 API，用于访问绝对地址 DB、输入、输出、
标志位、定时器和计数器。

本仓库是对 `robinson/gos7` 的一次有意不兼容重构。项目继续采用 BSD-3-Clause
并保留上游署名，同时删除旧 Handler、AG/PG、CPU 控制、NCK、MPI 和 PPI API。

> **当前状态：**`v0.1.0` 是新 API 的第一个源码版本。真实控制器资格验证已延期，
> 不宣称任何硬件型号已经认证；v1.0.0 前 API 仍可能调整。

## 平台支持

- 源码最低使用 Go 1.20；CI 同时测试 Go 1.20.14 和当前 stable；
- 不使用 CGO，不依赖本地动态库；
- 使用 Go 1.20.x 构建时以 Windows 7 amd64 为编译目标；
- Windows amd64/arm64、Linux amd64/arm64/ARMv7。

Go 1.20 是最后能够生成 Windows 7 兼容二进制的 Go 版本，因此保留 1.20.14 作为
legacy 构建线；它已不再获得 Go 安全修复，当前 stable Go 才是 test、race、fuzz
和漏洞扫描的主运行时。使用更高版本 Go 编译相同源码，**不会**继续保留 Windows 7
运行兼容性。Windows 7 实机资格验证已延期，且不支持 Windows 7 ARM64。

## 已实现范围

- TCP、TPKT、COTP、Setup Communication、ReadVar 和 WriteVar；
- rack/slot 或显式 TSAP 寻址、IPv4/IPv6、本地 IP 绑定、keepalive、连接 deadline、
  单次 exchange deadline 和 context 取消；
- TPDU/PDU 协商、240～960 请求 PDU、单个线请求最多 20 item、受检批量装箱、
  大读取拆分重组和显式连续区域分片；
- 与输入对齐的单点结果、结构化错误和明确的写入结果；
- 原生 bit 写入，不在后台做读改写；
- 绝对地址解析/格式化，以及受检整数、REAL/LREAL、经典 STRING、Counter 和
  S5TIME codec；
- 不产生 PLC 流量的本地诊断。

每个 `Client` 只拥有一个物理 S7 会话。可以并发调用，但完整逻辑操作会在该会话
上串行执行。`Close` 幂等且为终态。

本库明确**不负责**重试、自动重连、轮询、连接池、Tag Cache、deadband、地址
合并、设备发现、CPU 控制、block 管理、密码操作和 NCK。这些策略属于应用或驱动
运行时。

## 安装

```text
go get github.com/punk-one/gos7@v0.1.0
```

## 快速使用

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
	return fmt.Errorf("读取失败：batch=%v results=%v", batchErr, results)
}
value, err := codec.Float32(results[0].Data)
```

可直接运行的完整程序：

- [批量读取 REAL 和 BOOL](../../../examples/batch-read/main.go)；
- [带授权和明确写结果的 WORD 写入](../../../examples/safe-write/main.go)；
- [运行命令与安全说明](../../../examples/README.md)。

`New` 只校验配置，不产生网络 I/O；`Dial` 会依次执行 `New` 和 `Connect`。
endpoint 未填写端口时使用 TCP 102。`ExchangeTimeout` 只限制单次请求/响应；调用方
context 限制包含排队和全部分片在内的完整逻辑操作。

## 地址和数据

| 地址示例 | 含义 |
| --- | --- |
| `DB1.DBX0.7`、`DB1.DBB2`、`DB1.DBW4`、`DB1.DBD8` | DB bit/byte/word/dword |
| `I0.0`、`IB2`、`IW4`、`ID8` | 输入（也接受 `E` 别名） |
| `Q0.0`、`QB2`、`QW4`、`QD8` | 输出（也接受 `A` 别名） |
| `M0.0`、`MB2`、`MW4`、`MD8` | 标志区 |
| `T12`、`C7` | 定时器和计数器 |

地址宽度只是位置提示，不是业务类型。`DB1.DBD4` 可以作为 DWORD、DINT、REAL，
也可以是 8 字节 LREAL 的起点。LREAL 使用 `TransportByte`、count 8；经典 STRING
使用 `TransportByte`、count `2 + capacity`。字符编码、缩放、单位、质量和时间戳
属于应用策略。

## 结果和重连规则

`Read` 和 `Write` 结果始终与输入顺序对齐。必须同时检查批次错误和每个 item；
某个点被 PLC 拒绝不会遮蔽同批次成功结果。

| 写入结果 | 含义 |
| --- | --- |
| `WriteNotAttempted` | 该 item 没有发出请求 |
| `WriteAcknowledged` | PLC 返回了匹配的成功响应 |
| `WriteRejected` | PLC 明确拒绝 |
| `WriteUnknown` | 请求可能已生效，但没有收到可信响应 |

不得自动重试 `WriteUnknown`，必须先核对 PLC 或工艺状态。传输、超时或协议错误可能
将会话标记为 broken；应用策略确认安全后，可以对尚未关闭的 client 再次调用
`Connect`。本库不会隐式重连。

`ReadArea`/`WriteArea` 用于明确允许分片的 DB/I/Q/M 连续字节区域。失败时返回的
`n` 只表示已确认的连续前缀。单个有类型值应优先使用普通 `Write`。

## 仓库结构

```text
gos7/                       公共会话、item、错误和诊断 API
codec/                      公共、受检的 Siemens 值编解码
internal/isotcp/            有界 TPKT/COTP framing
internal/protocol/          S7comm 请求/响应编解码
internal/testplc/           仅供测试使用的离线 scripted PLC
docs/                       公开文档和多语言翻译
examples/                   可运行的公共 API 示例
test/                       公共 API 黑盒测试
test/qualification/         显式开启的真实 PLC 资格测试
```

根包保留一个公共 API 生命周期黑盒测试，使普通根包工具始终执行测试。更完整的
公共 API 测试、地址 fuzz 和端到端 benchmark 位于 `test/`；底层 ISO-on-TCP、
protocol 和 codec 测试继续与各自子包实现放在一起。

## 测试和资格验证

默认测试为纯 Go 且完全离线：

```powershell
$env:GOWORK = 'off'
$env:CGO_ENABLED = '0'
go test ./...
go vet ./...

# 可选：从现代 Go 命令验证 Windows 7 legacy 工具链。
$env:GOTOOLCHAIN = 'go1.20.14'
go test ./...
```

真实 PLC 测试默认只读，要求 `s7integration` tag，并拒绝仓库内配置。参见
[真实 PLC 资格测试说明](../../../test/qualification/README.md)。写入资格测试同时要求
完全匹配的授权值和精确目标白名单。

## 控制器范围和安全

v0.1 的设计目标包括 S7-300/400 经典绝对地址访问、启用 PUT/GET 且使用标准
non-optimized DB 的 S7-1200/1500，以及对准确 CPU 配置完成资格验证后的
S7-200 SMART Ethernet。这些是目标范围，不表示所有型号已经通过真实硬件测试。

本版本不支持 S7comm Plus、符号/optimized access、受保护 TLS 会话、S7 routing、
MPI 和 PPI。经典 S7comm 不具备现代传输安全能力，只能在经过授权、已完成网络
分区的 OT 网络中使用，并将 PLC 写权限限制到最小。

## 许可

项目采用 BSD-3-Clause，参见 [LICENSE](../../../LICENSE)、[NOTICE](../../../NOTICE) 和
[CHANGELOG.md](../../../CHANGELOG.md)。Siemens、SIMATIC 和 S7 名称是 Siemens AG 的商标。
本项目为独立开源项目，与 Siemens 没有隶属或背书关系。
