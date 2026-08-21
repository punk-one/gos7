# gos7 实现与发布规范

状态：Implemented Baseline v4（`v0.1.0`）

更新时间：2026-08-21

文档角色：当前项目实现规范（维护者与 LLM 的首要项目上下文）

适用模块：`github.com/punk-one/gos7`

Go module 基线：Go 1.20

原 fork 审计基线：`a8deeeeb454c56ee131090cdd4efa2837b7b7fdd`

> 本文件自包含地描述当前工作树已经实现的行为、明确边界、源码提交条件和延期验证，
> 供维护者与 LLM 理解和修改项目时优先读取。它不是已发布兼容性承诺；根
> `README.md` 仍是面向用户的公开概览。实现、测试或发布状态发生变化时，必须在
> 同一变更中同步本文件；若出现临时偏差，以可复现的代码和测试证据为准，并把
> 文档偏差视为待修复缺陷。

## 1. 当前结论

gos7 已经从旧的巨型 Handler/AG/PG API 重构为纯 Go、单物理会话、支持取消和
受检批处理的经典 Siemens S7comm/ISO-on-TCP 数据面库。v0.1 不提供旧 API
兼容层，应用必须直接适配当前公共 API；尚不能把“协议代码可用”表述为“所有目标
PLC 已完成生产资格认证”。

### 1.1 实现状态

| 能力 | 当前状态 | 证据/边界 |
| --- | --- | --- |
| module、许可、归属 | 已实现 | `github.com/punk-one/gos7`，BSD-3-Clause，根目录 `LICENSE`/`NOTICE` |
| Go 1.20、无 CGO | 已实现 | Go 1.20.14 test/vet；当前 Go stable 为质量线；无第三方 module |
| 五平台编译 | 已实现 | Windows amd64/arm64、Linux amd64/arm64/ARMv7 |
| Windows 7 | 编译目标已满足，实机待验证 | 仅 amd64，且二进制必须由 Go 1.20.x 构建 |
| ISO-on-TCP/S7comm | 已实现 | TPKT、动态 TPDU proposal、COTP CR/CC/DT、Setup、ReadVar、WriteVar |
| 单 session 状态机 | 已实现 | Context、deadline、串行 gate、Close 中断、显式重连 |
| batch/PDU planner | 已实现 | 单 PDU 最多 20 item、流式装箱、大 read/Area 分片、fragment 上限 |
| 地址与 codec | 已实现 v0.1 范围 | DB/M/I/Q/T/C；Bool、整数、REAL/LREAL、STRING、Counter、S5TIME |
| 结构化错误/写结果 | 已实现 | `errors.Is/As`、`SessionImpact`、四种 `WriteOutcome` |
| 默认离线测试 | 已实现 | root/`test` 黑盒、scripted PLC、golden、7 个 fuzz target、benchmark |
| 真实 PLC 测试入口 | 已实现 | `s7integration`、外部 JSON、默认只读、双重写入门禁 |
| 真实 PLC 型号矩阵 | 延期、非 v0.1 阻断项 | S7-200 SMART、S7-300/400、S7-1200/1500 尚无正式结果，不作硬件兼容承诺 |
| 首个公开 tag | 本发布提供 | `v0.1.0` 指向本规范描述的源码基线 |

### 1.2 不可破坏的核心原则

1. gos7 只负责一个物理 S7 session、协议编解码、输入验证和 PDU 规划。
2. 所有可能阻塞的公共网络操作必须接受非 nil `context.Context`。
3. 一个 `Client` 对并发调用安全，但同一物理 session 上的完整逻辑操作串行。
4. 普通 `Write` 不拆分单个 item，也不透明重试任何写入。
5. `WriteUnknown` 可能已经作用于 PLC，调用方必须先 reconciliation，禁止自动重放。
6. 点位合并、轮询、缓存、deadband、quality、重连退避和连接池属于上层。
7. 地址只描述位置和 wire width；值类型、字符编码、缩放、单位不由地址猜测。
8. 默认日志、错误和诊断不得包含 PLC payload 或业务值。
9. gos7 不依赖任何 Punk module、native library 或 CGO。
10. 当前没有稳定版本，不保留旧 API wrapper 或双协议栈。
11. `ExchangeTimeout` 只限制单次 request/response；完整逻辑调用由调用方 context 限制。

## 2. 平台、工具链与仓库结构

### 2.1 平台约束

- `go.mod` 为 `go 1.20`，CI 同时测试 Go 1.20.14 legacy 线和当前 stable；
- 生产和资格测试编译均使用 `CGO_ENABLED=0`；
- Windows 7 只支持 amd64，不存在 Windows 7 ARM64 承诺；
- 使用 Go 1.21 或更高工具链构建的 Windows 二进制不再具有 Windows 7 运行承诺；
- Windows ARM64 指现代 Windows；Linux 支持 amd64、arm64 和 ARMv7；
- Go 1.20 已停止上游维护，Windows 7 制品应视为受限兼容产物，部署在隔离 OT
  网络并单独执行安全评估；
- 当前 module 没有外部依赖，因此 `go mod tidy` 不生成 `go.sum`。

### 2.2 当前目录

```text
gos7/
  README.md                 # 英文规范入口
  LICENSE / NOTICE          # 许可和上游归属
  CHANGELOG.md              # 未发布/发布版本变更
  client.go                 # Client、session、Read/Write、planner
  config.go                 # Config、寻址、默认值、资源上限
  address.go                # ParseAddress / FormatAddress
  item.go                   # Area、Address、Transport、item/result
  errors.go                 # ErrorKind、Error、SessionImpact
  diagnostics.go            # State、SessionLimits、Diagnostics
  codec/                    # 公共、受检的值 codec
  internal/isotcp/          # TPKT/COTP framing
  internal/protocol/        # Setup/ReadVar/WriteVar wire codec
  internal/testplc/         # 只供测试使用的 loopback scripted PLC
  examples/                 # 可运行 batch-read / safe-write
  test/                     # 公共 API 黑盒测试、fuzz、benchmark
  public_api_test.go        # 根包公共 API 生命周期黑盒测试
  test/qualification/       # 带 s7integration tag 的真实 PLC 测试
  docs/                     # 受版本控制的项目文档
    spec.md                 # 当前实现规范；维护者与 LLM 的主要上下文
    i18n/<locale>/          # README 翻译
```

根包保留一个公共 API 生命周期黑盒测试；完整公共 API、fuzz 和 benchmark 位于
`test/`。底层 `codec`、`isotcp`、`protocol` 的白盒测试与各自子包同目录。不得
为了目录层次新建只含转发类型的 `internal/session` 或 `internal/transport` 包。

### 2.3 仓库级文档规则

- 根目录固定保留 `README.md`、`LICENSE`、`NOTICE`、`CHANGELOG.md`；
- 根 README 是功能、平台和安全声明的英文规范来源；
- `docs/spec.md` 是当前实现、架构边界和发布准备的详细规范来源，必须与代码和测试
  同步，且应作为 LLM 处理本项目时的首要上下文；
- 翻译统一放在 `docs/i18n/<BCP47-locale>/README.md`；
- 每份翻译必须声明 locale 与同步基线，并登记到 `docs/README.md`；
- 不再向根目录添加 `README.zh-CN.md`、`README.ja-JP.md` 等文件。

## 3. 技术架构和责任边界

```text
Public API
  Client / Config / Address / Item / Result / Error / Diagnostics
        |
Validation + private batch planner
  limits + streaming request/response sizing + read/area chunking
        |
internal/protocol
  Setup Communication + ReadVar + WriteVar
        |
internal/isotcp
  bounded TPKT + COTP CR/CC/DT
        |
Go net package
  TCP + deadline + cancellation interrupt + local bind + keepalive
        |
Siemens PLC
```

### 3.1 gos7 负责

- 连接一个 endpoint，完成 TCP、COTP 和 S7 Setup Communication；
- 校验 rack/slot 或显式 TSAP 配置；
- 校验 item/address/count/data 和调用方资源上限；
- 使用当前 session 的 negotiated TPDU/PDU 进行双向装箱和 frame 约束；
- 将每个 ReadVar/WriteVar wire PDU 限制为最多 20 item；
- 拆分并重组大 read；
- 对显式 `WriteArea` 拆分连续 byte 写并报告已确认前缀；
- 保留全局和逐项 PLC error code；
- 报告本地 session state、limits、地址和最后失败分类；
- 在取消、超时、传输或协议错误后停止不安全的 session 复用。
- 对每次 exchange 设置独立 timeout，由 caller context 约束整个逻辑调用。

### 3.2 gos7 不负责

- 自动 reconnect、backoff、retry、pool、lease、idle close；
- polling、点位连续区自动合并、cache、deadband、quality、timestamp；
- controller discovery、SZL/model probe、symbolic browse；
- CPU Run/Stop、block upload/download、目录、安全密码、时钟、NCK；
- S7comm Plus、optimized/symbolic DB、TLS/protected session；
- MPI、PPI、S7 routing、冗余 CPU failover；
- scale、precision、unit、文本编码或业务幂等；
- 生命周期日志、metrics、trace 和 payload/value 观测；
- raw PDU escape hatch。

## 4. 公共 API 规范

### 4.1 构造和会话 API

```go
func New(config Config) (*Client, error)
func Dial(ctx context.Context, config Config) (*Client, error)

func (c *Client) Connect(ctx context.Context) error
func (c *Client) Close() error
func (c *Client) State() State
func (c *Client) Limits() (SessionLimits, bool)
func (c *Client) Diagnostics() Diagnostics
```

- `New` 只归一化、校验并复制配置，不发起网络 I/O，初始状态为 `StateNew`；
- `Dial` 等价于 `New` 后 `Connect`；连接失败时关闭内部 client 且不返回该实例；
- `Connect` 在 `StateReady` 时幂等返回，不做可达性探测；
- `StateBroken`/`StateDisconnected` 的非 closed client 可由调用方显式再次 `Connect`；
- `Close` 幂等、终态，会取消生命周期 context、关闭 socket 并解除阻塞；
- nil `Client` 的 `Close` 成功，`State`/`Diagnostics` 表现为 closed；其他 nil receiver
  数据面调用返回参数错误；
- 未经 `New` 构造的零值 `Client{}` 不 panic：Connect/data API 安全失败，Close 安全终结；
- `Close` 返回底层 socket close error 时不保证该错误属于 `*gos7.Error`。

### 4.2 数据面 API

```go
func (c *Client) Read(ctx context.Context, items []ReadItem) ([]ReadResult, error)
func (c *Client) Write(ctx context.Context, items []WriteItem) ([]WriteResult, error)

func (c *Client) ReadArea(
    ctx context.Context, area Area, dbNumber uint16, start uint32, dst []byte,
) (n int, err error)

func (c *Client) WriteArea(
    ctx context.Context, area Area, dbNumber uint16, start uint32, src []byte,
) (n int, err error)
```

- 所有接受 context 的网络/数据面 API 都拒绝 nil context；已经取消的 context 不启动 I/O；
- `Read`/`Write` 在获取 session gate 前完成 caller input 校验；
- `Write` 在任何 I/O 前校验完整 batch，并复制所有 `WriteItem.Data`；
- accepted-size batch 的 result 与输入顺序对齐；pre-canceled context 或 item 数超过
  `MaxItemsPerCall` 时可以返回 nil results；
- `ReadArea`/`WriteArea` 只适用于 DB/I/Q/M 连续 byte 范围；Timer/Counter 使用 item API；
- `ReadArea` 直接写调用方 `dst`，不建立同尺寸中间结果；`n` 是连续确认前缀；
- `WriteArea` 的 `n` 是 PLC 已明确 acknowledged 的连续前缀，不包含 unknown chunk。

## 5. Config、寻址和默认值

### 5.1 Config

```go
type Config struct {
    Endpoint            string
    Addressing          Addressing
    LocalAddress        string
    LocalPort           uint16
    RequestedPDU        uint16
    MaxFrameBytes       int
    MaxItemsPerCall     int
    MaxFragmentsPerCall int
    MaxItemBytes        int
    MaxBatchBytes       int
    ConnectTimeout      time.Duration
    ExchangeTimeout     time.Duration
    KeepAlive           time.Duration
}
```

| 字段 | 零值默认 | 当前约束 |
| --- | --- | --- |
| Endpoint port | 102 | host 必填；显式端口为 1..65535；IPv6+port 使用方括号 |
| RequestedPDU | 480 | 240..960，且 `PDU + 7 <= MaxFrameBytes` |
| MaxFrameBytes | 4096 | 32..65535 |
| MaxItemsPerCall | 4096 | 1..1,048,576 |
| MaxFragmentsPerCall | 65,536 | 1..1,048,576 |
| MaxItemBytes | 1 MiB | 1..1 GiB |
| MaxBatchBytes | 32 MiB | 不小于 MaxItemBytes，最大 2,147,483,647 |
| ConnectTimeout | 10s | 不得为负 |
| ExchangeTimeout | 10s | 不得为负；每次 exchange 重新计时 |
| KeepAlive | 30s | 不得为负 |

三个 duration 的零值均表示使用默认值，不表示禁用；v0.1 当前没有公开的“禁用
timeout/keepalive”哨兵。Endpoint 可为 DNS 名、IPv4 或 IPv6 literal，支持 IPv6
zone。所有 endpoint 分支在调用 `net` 前统一拒绝 NUL、斜杠和反斜杠；
`LocalAddress` 必须是 IP literal。设置 `LocalPort` 时必须同时设置
`LocalAddress`，port 0 表示由 OS 选择。`MaxItemsPerCall` 是逻辑 API 输入上限，
不是 wire PDU item 上限；后者固定为 20。

### 5.2 两种互斥寻址

```go
type Addressing struct {
    Mode       AddressingMode
    Rack       uint8
    Slot       uint8
    Role       ConnectionRole
    LocalTSAP  uint16
    RemoteTSAP uint16
}
```

Rack/slot 模式：

- `Rack` 为 0..7，`Slot` 为 0..31；
- `Role` 必须为 `ConnectionPG(1)`、`ConnectionOP(2)` 或
  `ConnectionBasic(3)`；它是 COTP connection-resource role，不是 PLC 型号或权限；
- LocalTSAP/RemoteTSAP 必须为零；
- LocalTSAP 生成 `0x0100`，RemoteTSAP 由 role/rack/slot 生成。

显式 TSAP 模式：

- LocalTSAP/RemoteTSAP 都必须非零；
- Rack/Slot/Role 必须保持零；
- 两种模式不得混填，也没有隐式 fallback。

## 6. 地址、Transport 和 codec

### 6.1 地址模型

```go
type Address struct {
    Area     Area
    DBNumber uint16
    Offset   uint32
    Bit      uint8
}
```

- DB/I/Q/M 的 `Offset` 是 byte offset；Timer/Counter 的 Offset 是 element index；
- DB area 要求非零 DBNumber；其他 area 的 DBNumber 必须为零；
- bit 为 0..7，非 bit transport 的 Bit 必须为零；
- wire address 必须适配 S7 的 24-bit 地址字段；
- DB/I/Q/M 校验完整 byte span；Timer/Counter 校验 `Offset + Count - 1` 的完整
  element span，不能只校验起点；
- `TransportBit` 在 v0.1 要求 count 恰好为 1；
- 所有 count 必须大于零。

`ParseAddress` 接受大小写不敏感、可带前导 `%`、可有首尾空白的有限语法：

| 形式 | 示例/别名 | canonical 输出 |
| --- | --- | --- |
| DB bit | `DB1.DBX0.7` | `DB1.DBX0.7` |
| DB byte/word/dword | `DB1.DBB2` / `DBW4` / `DBD8` | 同宽度输出 |
| Input | `I0.0`、`EX0.0`、`IB/IW/ID` | `IX/IB/IW/ID` |
| Output | `Q0.0`、`AX0.0`、`QB/QW/QD` | `QX/QB/QW/QD` |
| Marker | `M0.0`、`MB/MW/MD` | `MX/MB/MW/MD` |
| Timer | `T12`、`TM12` | `T12` |
| Counter | `C7`、`CT7`、`Z7` | `C7` |

不支持 symbolic/TIA path。地址宽度只是 wire/location hint：`DB1.DBD4` 可以承载
DWORD、DINT、REAL，也可以作为八字节 LREAL 的起点。

### 6.2 Transport

| Transport | element bytes | 当前规则 |
| --- | ---: | --- |
| `TransportBit` | 1 public byte | count=1；写入数据只能为 0/1；使用原生 S7 bit write |
| `TransportByte` | 1 | BYTE/SINT、raw span、LREAL/STRING carrier |
| `TransportWord` | 2 | WORD/INT |
| `TransportDWord` | 4 | DWORD/DINT |
| `TransportReal` | 4 | Siemens REAL response transport |
| `TransportTimer` | 2 | 只允许 AreaTimer |
| `TransportCounter` | 2 | 只允许 AreaCounter |

### 6.3 当前 codec

`codec` 使用 Siemens/network big-endian，要求 scalar buffer 长度恰好匹配：

- `Bool`/`PutBool`、`Bit`/`SetBit`；
- Int8/16/32/64、Uint8/16/32/64 及对应 Put；
- REAL `Float32`/`PutFloat32`；
- LREAL `Float64`/`PutFloat64`；
- classic STRING `DecodeClassicString`/`EncodeClassicString`；
- Counter BCD `DecodeCounter`/`EncodeCounter`；
- S5TIME `DecodeS5Time`/`EncodeS5Time`。

具体语义：

- Float codec 保留 IEEE-754 bit pattern，包括 NaN/Inf；是否允许由上层决定；
- LREAL 使用 `TransportByte`、count=8；
- STRING buffer 长度必须恰好为 `2 + declared capacity`，capacity 为 0..254；
- STRING 不猜字符集，不截断；`clearUnused=true` 时清零未使用容量；
- Counter 只接受 0..999 合法 BCD；
- S5TIME 编码选择能精确表示且 count<=999 的最细 time base，否则返回 error；
- scale、precision、unit、quality、timestamp、text encoding 均不属于 codec。

当前未实现 DATE、TIME、TIME_OF_DAY、DATE_AND_TIME、DTL。新增这些类型必须经过
独立 API proposal，不得用不受检 legacy helper 直接恢复。

## 7. Session 生命周期、并发和取消

### 7.1 状态

```text
StateNew
   -> StateConnecting -> StateReady
          |                |
          v                v
   StateDisconnected   StateBroken
          \                /
           -- explicit Connect --> StateReady

any non-closed state -> StateClosing -> StateClosed
```

- connect/handshake 失败使 client 进入 `StateDisconnected`；
- active session 的 transport/protocol failure 使 client 进入 `StateBroken`；
- `StateReady` 仅表示本地保存了可用 socket/limits，不保证 PLC 此刻仍可达；
- successful Connect 增加 `SessionGeneration`，重置 last failure，并保存新 limits；
- `Close` 后任何 Connect/Read/Write 返回 closed，不能复活该实例。

### 7.2 串行 gate

- Connect/Read/Write/Area 操作共享一个可取消 gate；
- 排队者可以因自己的 context 或 Client Close 退出；
- 不使用不可取消的 `sync.Mutex.Lock` 作为 API 排队入口；
- 当前 Setup 请求 MaxAmQCaller/MaxAmQCallee 都为 1，即使 PLC 支持更高并发也不
  pipeline 多个 S7 job；
- 每个 session 的 PDU reference 为非零递增 uint16，溢出后跳过零。

### 7.3 Go 1.20 取消与 timeout 实现

由于不能使用 Go 1.21 `context.AfterFunc`：

- 每个逻辑 operation 使用受生命周期 context 驱动的短期 watcher；cleanup 必须使其退出；
- ConnectTimeout 覆盖连接握手；ExchangeTimeout 在每个 data-plane exchange 开始时
  单独创建，不覆盖整个多 PDU 调用；caller context 仍覆盖排队和全部 fragment；
- socket I/O 设置当前 exchange context deadline；context 提前取消时 watcher 将 deadline
  推到当前时间以解除阻塞；
- 成功/失败路径都必须停止 watcher 并清理 socket deadline；
- 不存在后台 reconnect、ping 或永久 polling goroutine。

## 8. Read、Write 和 PDU 语义

### 8.1 Read

- 完整 batch 在 I/O 前验证，总 logical bytes 受 MaxBatchBytes 限制；
- planner 同时限制 request PDU 和预计 AckData response PDU；
- wire codec 能表达 1..255 item，但 client planner 为主流 PLC 兼容性将每个 PDU
  固定限制为最多 20 item；21 个及以上 logical item 自动形成后续 PDU；
- read fragment 数在分配输出 buffer 和 I/O 前受 MaxFragmentsPerCall 限制；planner
  按 batch 增量生成，不预先物化全部 fragment/`[][]batch`；
- 大 byte/word/dword/real read 可按 element 边界分片，最后按原输入重组；
- result 始终对应原 input index；
- 单个 PLC item rejection 写入该 `ReadResult.Err`，不会遮蔽成功 sibling；
- 已确认前缀保留；fatal transport/protocol failure 后 Data 截断到确认进度；
- per-item PLC rejection 通常不产生 batch error；global/fatal error 产生 batch error。
- 一个 logical item 的 fragment 被 PLC 拒绝后，不再发送该 item 的剩余 fragment；
  同批成功 sibling 仍保留。

多个 PDU 不构成 PLC 原子事务，也不保证同一扫描周期快照。

### 8.2 Write

- 完整 batch 的 address/count/data 在任何 I/O 前校验，payload 随后 clone；
- 普通 `WriteItem` 从不拆分；所有 item 都先按 negotiated PDU 预检，任一放不下时
  整个调用在发送前失败，不会先写入排在前面的 item；
- 多个可容纳 item 可装入同一 PDU，超出后按 item 边界形成下一 PDU；
- default `Write` 遇到单个 item rejection 仍保留/处理其他 item；
- global PLC rejection 将当前 batch 标为 rejected，未开始的后续 item 为 not attempted；
- request 未发送时为 not attempted；request 可能部分/完整发送但没有可信 response
  时为 unknown；匹配响应 decode 失败时当前 batch 也为 unknown；
- 之前已 acknowledged 的 item 保持 acknowledged。

### 8.3 WriteOutcome

| Outcome | 可观察事实 | 自动重试 |
| --- | --- | --- |
| `WriteNotAttempted` | 该 item 没有开始发送 | 由上层策略决定 |
| `WriteAcknowledged` | 收到匹配成功响应 | 不需要 |
| `WriteRejected` | PLC 明确拒绝 | 默认不重试，先处理配置/权限 |
| `WriteUnknown` | 可能已发送/生效但无法确认 | 严禁自动重放 |

`Temporary=true` 只描述 transport/timeout 分类提示，不表示写入可重试。

### 8.4 Area API

- `ReadArea` 显式允许单一 DB/I/Q/M byte range 跨 PDU；
- `ReadArea` 的 response data 直接复制到 `dst`，每段 response 不再先 clone；
- `WriteArea` 显式允许非原子、按 chunk 流式写入，每个 chunk 只 clone 一次并单独确认；
- 第一个 rejected/unknown/fatal chunk 后停止后续 chunk；
- `n` 只计算从 start 开始连续 confirmed/acknowledged 的 byte 数；
- 对单个 typed value 应使用普通 Write，而不是 WriteArea。

## 9. ISO-on-TCP 与 S7comm 实现

### 9.1 TPKT/COTP

- TPKT version 必须为 3、reserved byte 为 0；
- 读取四字节 header 后先检查声明长度 7..MaxFrameBytes，再分配 frame；
- socket 写通过完整写循环处理 short write 和 no-progress；
- COTP CR 使用显式 local/remote TSAP，并提出能够承载请求 S7 PDU 的 TPDU size；
- CC 校验 PDU type、destination reference、class、参数长度；TSAP 参数存在时必须
  与 CR 反向匹配；TPDU 参数存在时解析并取 peer/offered 的较小值，缺失时保留
  offered limit；
- 当前 240..960 的请求范围提出 1024-byte TPDU；每个 DT 在发送和接收时都检查
  `COTP header + S7 PDU <= NegotiatedTPDU`；
- DT 必须为 `LI=2`、type `0xf0` 且 EOT=1；
- 当前**不重组 fragmented COTP DT**，EOT=0 返回 `ErrCOTPFragmented` 并使
  active session 失效。不得在文档中声称支持 COTP reassembly。

### 9.2 Setup Communication

- request PDU reference=1，MaxAmQ caller/callee=1；
- response 严格校验 protocol ID、ROSCTR、reference、parameter/data 长度；
- global error class/code 保留为 PLC error；
- Setup proposal 先受 negotiated TPDU payload 限制；negotiated S7 PDU 必须 >=240、
  不大于实际 proposal，并同时适配 negotiated TPDU 和 MaxFrameBytes；
- negotiated MaxAmQ 必须非零且不能超过当前实现请求的 1；
- Connect 不发送 SZL、controller detection、ping 或其他隐式 job。

### 9.3 ReadVar/WriteVar

- 只编码 S7ANY variable item；
- wire codec 校验非零 reference、1..255 items、wire area、amount、24-bit address 和
  length；Client planner 单 PDU 最多提交 20 item；
- AckData 校验 redundancy ID、reference、global code、function、item count、transport、
  encoded length、padding 和 trailing data；
- failed read item 必须为零 transport，接受常见的 `length=0` 或 `length=4` 无 payload
  header，其他伪 data/length 仍拒绝；
- protocol parser 的成功 data slice 引用 response frame；Client 只向最终 public result
  或 ReadArea `dst` 复制一次，返回后调用方不引用 frame backing array。

## 10. 错误、诊断和观测

### 10.1 Error

```go
type Error struct {
    Op         string
    Kind       ErrorKind
    Code       uint32
    ReturnCode byte
    Temporary  bool
    Impact     SessionImpact
    Cause      error
}
```

当前 ErrorKind：

```text
invalid_argument  not_connected  timeout  canceled  transport
protocol          plc            limit    closed
```

- 每个 kind 有对应 `Err*` sentinel，可通过 `errors.Is` 判断；
- 详细字段通过 `errors.As(err, *Error)` 获取；
- PLC global code 写入 Code；item return code 同时写入 Code/ReturnCode；
- Error 字符串中的 cause 正文最多保留前 256 字符；超长时再追加省略号；
- validation/PLC item error 通常 `SessionUnchanged`；active transport/protocol failure
  为 `SessionBroken`；
- 本地 encoder/planner invariant error 为 `SessionUnchanged`，不得仅因本地 bug 把
  已对齐 session 标为 broken；对端 malformed response 才是 session-breaking protocol error；
- 未使用的 `ErrorUnsupported`/`ErrUnsupported` 已在 v0.1 发布前删除；
- error 不包含 ReadResult.Data 或 WriteItem.Data。

### 10.2 Diagnostics

`Diagnostics()` 只读本地内存，不执行 network probe：

- State；
- SessionGeneration；
- 实际 Setup proposal、negotiated PDU、negotiated TPDU 与 MaxAmQ；
- LimitsValid；
- LocalAddress/RemoteAddress；
- LastActivity；
- LastSessionFailureKind（只记录 connect 或 active session failure，成功 Connect 时清空）。

`Limits()` 只在 StateReady 且 limits valid 时返回 true。地址属于授权调用方可见诊断，
但 core 不主动生成日志。

### 10.3 观测边界

- v0.1 core 不提供 `Logger`、`WithLogger` 或内建 metrics/trace；生命周期两条日志不足以
  构成驱动能力，且会与应用层设备上下文、采样和脱敏策略重复；
- 应用层基于调用结果、耗时、`Diagnostics()` 和自己的 device/resource ID 记录日志、
  metrics 与 trace；
- Diagnostics 只暴露 session 地址、状态和 limits；typed error 与默认行为均不包含
  PLC payload、WriteItem.Data 或 decoded value。任何未来 raw trace 必须是独立的
  安全 proposal。

## 11. 资源与安全

### 11.1 资源边界

- TPKT 分配受 MaxFrameBytes 限制；
- public item/batch 受 MaxItemsPerCall、MaxItemBytes、MaxBatchBytes 限制；
- protocol wire 字段上限为 255，但 Client 每个 wire PDU 的兼容上限为 20；
- public logical item 数受 MaxItemsPerCall 限制；read fragment 和 `WriteArea` chunk
  分别在 I/O 前受 MaxFragmentsPerCall 限制；
- read 和 write batch planner 增量生成下一批，不建立全部 `[][]batch`；ReadArea 直接
  写 `dst`，WriteArea 逐 chunk clone/发送；
- 所有 length/count/span 算术在转换和分配前检查 overflow；
- Read output 和 cloned Write input 的总量受 batch limit 约束；
- 没有无界 background queue、reconnect queue 或 connection cache。

### 11.2 安全事实

- 经典 S7comm/PUT-GET 没有现代 TLS 机密性或对等认证；
- 只能在授权、分区的 OT 网络中使用，并通过 ACL/VPN/安全网关保护；
- S7-1200/1500 必须由现场显式启用 PUT/GET 和 standard/non-optimized DB；
- gos7 不绕过 PLC 安全配置，不尝试降级到不安全模式；
- 错误地址、类型和值可能影响设备、财产和人员，写权限必须最小化；
- safe-write 示例的 consent 只是防误操作门槛，不是认证或授权系统；
- gos7 Config 不接受密码/SecretRef，也没有 raw packet logging。
- endpoint 在任何 `SplitHostPort`/Dial 分支前拒绝 NUL、斜杠和反斜杠。该检查同时
  缩小 Windows `net` 的危险输入面，但不能替代使用仍受安全维护的 Go 工具链；
- Go 1.20.14 Windows 7 binary 是明确的 legacy 兼容制品，不应作为通用安全基线。

## 12. 测试和质量证据

### 12.1 默认离线测试

`go test ./...` 只使用进程内逻辑和 `127.0.0.1` loopback scripted PLC，不访问真实
PLC、固定私网地址、公网、ICMP 或 native DLL。

当前覆盖：

- config 默认/合法/非法、IPv4/IPv6/DNS、显式端口 NUL/路径字符、寻址互斥、资源限制；
- address corpus、canonical round-trip、非法/越界、LREAL raw span；
- TCP+COTP+Setup、TPDU proposal/peer reduction、显式 TSAP、本地 bind、PDU 240/480/960；
- batch read/write、20/21 item 边界、native bit、mixed per-item PLC failure；
- request/response PDU 分片、ReadArea/WriteArea、partial confirmed prefix；
- fragment limit、失败 large-read item 停止剩余 fragment、Timer/Counter 完整 span；
- Unknown write 不重试、broken session 显式重连；
- caller/exchange timeout、queue cancellation、Close 中断、零值 Client、validation-before-I/O；
- codec round-trip、错误 buffer、STRING 128、bit 邻位、Counter/S5TIME；
- TPKT/COTP/protocol golden、short read/write、padding、reference 和 trailing data。

### 12.2 当前 fuzz 目标

```text
FuzzAddressParser
FuzzClassicString
FuzzDecodeTPKT
FuzzDecodeCOTP
FuzzDecodeSetupCommunication
FuzzDecodeReadVarResponse
FuzzDecodeWriteVarResponse
```

这些 fuzz 保证 parser 对任意输入不 panic；它们不等价于“每个 scalar codec 都有
独立 fuzz”。后续可补充 scalar/Config fuzz，但不是当前已完成事实。

### 12.3 Benchmark

`test/benchmark_test.go` 通过公共 `Client` 和 loopback PLC 覆盖：

- 1/20/21/100/1000/4096 logical item batch read；
- 1 MiB continuous ReadArea；
- 完整 validation/planner/protocol/socket pipeline。

结果用于回归比较，不是实际 PLC SLA，也不直接代表现场扫描周期。

### 12.4 CI 当前事实

workflow 对 branch/PR 和所有 `v*` tag 生效，分为三类 job：

1. test matrix：Go 1.20.14 与当前 stable，`CGO_ENABLED=0` 执行 `go test ./...`、
   `go vet ./...` 并编译 `s7integration` harness；
2. quality（stable Linux）：gofmt、`go test -race ./...`、根 gos7 包 coverage、7 个
   fuzz target 各 5 秒、`govulncheck ./...`；
3. cross-build（Go 1.20.14、无 CGO）：Windows amd64/arm64、Linux
   amd64/arm64/ARMv7。

CI 有 coverage 报告但尚无 threshold，也没有 SBOM；不得声称提交后的 CI 已通过，
直到远端 workflow 实际运行。Race 只属于具备 race toolchain 的 Linux 质量 job，
不改变生产构建无 CGO 的要求。

本工作树本地验证证据：

- Go 1.20.14：test/vet、qualification harness compile、五平台 cross-build；
- Go 1.25.10：test/vet、qualification harness compile；codec/isotcp/protocol/public
  suite `-count=10`；
- 根 gos7 包由 `./test` 驱动的 statement coverage 为 70.3%；
- 7 个 fuzz target 各执行 2 秒 smoke；workflow 经 actionlint 校验；
- Go 1.25.4 扫描暴露标准库 [GO-2026-4971](https://pkg.go.dev/vuln/GO-2026-4971)
  和 endpoint 校验缺口；修复输入边界后，
  使用已修复的 Go 1.25.10 执行 `govulncheck` 为调用路径零已知漏洞；
- 本机无 gcc，未执行 Windows race；不能用该事实替代 Linux CI race 结果。

## 13. 真实 PLC 资格测试

### 13.1 Harness 安全门禁

位置：`test/qualification/hardware_test.go`，build tag：`s7integration`。

- `GOS7_INTEGRATION_CONFIG` 必须为仓库外的绝对路径；symlink resolve 后再次检查；
- JSON 最大 64 KiB，`DisallowUnknownFields`，只允许单一 JSON value；
- 最多 4096 reads、256 echoWrites、256 allowlist；
- connect/exchange timeout 最大 300,000 ms；
- item name 必须受检且唯一；地址 canonical target 必须唯一；
- transport 可为 bit/byte/word/dword/real/timer/counter，count 默认 1；
- 默认只读；缺少配置时 skip，不会猜现场 endpoint；
- 日志只报告 target/name/byte count，不输出 payload。

Echo write 必须同时满足：

1. external JSON 中存在 `echoWrites`；
2. canonical `ADDRESS/transport/count` 精确出现在 `writeAllowlist`；
3. 环境变量精确为
   `GOS7_INTEGRATION_WRITE=ECHO-WRITE-TO-ALLOWLIST`；
4. 完整 `TestS7HardwareQualification` 先通过 read-only gate；
5. 每个 target 先读，原样写回相同 raw bytes，再读回比对；
6. 第一个 echo write 失败后停止后续写入。

不得用只匹配 `explicit-echo-write` 子测试的 `-run` 过滤器绕过 read gate。标准命令：

```powershell
$env:GOS7_INTEGRATION_CONFIG='D:\private\gos7-integration.json'
go test -tags=s7integration -run TestS7HardwareQualification -v ./test/qualification
```

### 13.2 延期的资格矩阵

至少需要：

- 一台现有场景使用的 S7-200 SMART；
- 一台 S7-300 或 S7-400；
- 一台启用 PUT/GET、使用 standard/non-optimized DB 的 S7-1200 或 S7-1500；
- rack/slot 和至少一个 explicit TSAP；
- 记录 CPU/固件/CP、协商 TPDU/PDU、MaxAmQ 和安全配置；
- DB/M/I/Q、bit、REAL、LREAL、STRING 128；硬件支持时再测 T/C；
- 1/10/20/21/100/1000 logical items、跨 PDU read 和连续 area；
- native bit write；不支持时记录设备事实，不在 gos7 静默 RMW；
- 错误 rack/slot、TSAP、DB、权限禁止、optimized DB；
- timeout、cancel、RST/拔网、PLC reboot、显式 reconnect；
- Unknown write 故障注入和人工/读回 reconciliation；
- 长稳运行、资源上限和多 PLC 并行；
- Go 1.20.14 构建的 Windows 7 amd64 实机 smoke test。

当前没有正式结果。这些项目不阻塞 v0.1.0 源码提交，但 README 只能使用“设计
目标”，不能宣称上述 PLC、固件或 Windows 7 运行环境已经认证。

## 14. 驱动层与应用层边界

```text
application
  value mapping + connection ownership + reconnect/backoff
  polling/coalescing/cache/quality + authorization/reconciliation
        |
application adapter
        |
gos7 Client
  one session + validation + TPDU/PDU + S7comm + ISO-on-TCP
        |
PLC
```

### 14.1 责任映射

| 上层概念 | 归属 |
| --- | --- |
| Host/Port、rack/slot、TSAP、timeouts | adapter 校验后映射 gos7 Config |
| connection resource、lease、shared client、idle close | 应用层 |
| reconnect/backoff/circuit breaker、安全读重试 | 应用层 |
| polling、two-phase read、bit/range merge、cache、deadband | 应用层 |
| TPDU/PDU negotiation、20-item packing、large read/Area chunk | gos7 |
| 应用值类型、长度和字符编码 | adapter 映射 Transport/Count + codec |
| LREAL | DBD 起点 + TransportByte/count 8 |
| STRING | TransportByte/count `2+capacity`，字符编码由应用决定 |
| scale/precision/unit/enum/quality/timestamp | 应用层 |
| write authorization/idempotency/readback | 应用层 |
| Unknown write | gos7 报告，应用禁止 replay 并触发 reconciliation |
| 日志、metrics、trace、MQTT/HTTP、离线队列 | 应用层 |

### 14.2 应用集成不变量

1. 应用不得自行计算 PDU header 或拆分 wire batch，logical item 直接交给 gos7；
2. bit 写使用 `TransportBit`，禁止应用内 byte read-modify-write；
3. 配置加载时一次性 `ParseAddress`，生成不可变 I/O plan；无效点位不得用零值 item
   填入 batch；
4. 按 `WriteOutcome` 处理写结果，`WriteUnknown` 必须 readback/人工 reconciliation；
5. connection manager 必须关闭失效连接、并发竞争中丢弃的连接、最后引用和应用停止
   时的 socket；
6. backoff 必须可由 context 取消；批次错误必须保留到最终结果，禁止解码零 buffer
   后上报成功；
7. 值类型决定 wire width，应用提供的长度只能作可选一致性检查；
8. 默认禁止 raw payload、参数和值日志；写入授权、幂等和审计由应用实现。

gos7 不 import 任何应用 contract、SDK 或业务 runtime，也不为旧 Handler/AG/PG API
提供 compatibility wrapper。应用端可修改时应直接适配当前 API。

## 15. 控制器目标与现场前置条件

| 控制器 | 目标范围 | 必要现场条件 | 不承诺 |
| --- | --- | --- | --- |
| S7-200 SMART | Ethernet/ISO-on-TCP 经典绝对访问 | 按实际 CPU 配置验证 rack/slot 或 TSAP、V/DB 映射和 PDU | 不硬编码所有型号的 slot/TSAP/V 映射 |
| S7-300/400 | DB/M/I/Q，硬件支持时 T/C | 正确 rack/slot/TSAP 和可用连接资源 | 不假设全部 CPU/CP 有相同 PDU/权限 |
| S7-1200/1500 | standard/non-optimized 全局 DB 和经验证的 M/I/Q | 启用 PUT/GET，正确用户/匿名权限 | 不支持 optimized/symbolic，不承诺传统 T/C area |

上述全部仍属于待执行资格矩阵。

## 16. 许可、版本和发布准备

### 16.1 许可与来源

- 保持 BSD-3-Clause 和原 `robinson/gos7` 上游版权；
- 根目录保留 LICENSE、NOTICE；
- 旧 vendor PDF、截图、二进制和来源不明资产不进入新发布；
- 完整 Git 历史保留原 fork 来源；
- Siemens、SIMATIC、S7 是 Siemens AG 商标，本项目独立且未获 Siemens 背书。

### 16.2 版本策略

- 首个公开 tag 为 `v0.1.0`；
- v0.x 允许根据协议、硬件和使用证据调整 API；
- v1.0 后遵循 Go module semantic versioning；
- 不创建旧 API compatibility wrapper；使用旧 API 的应用必须自行适配。

### 16.3 v0.1.0 源码提交准备

- [x] Go 1.20.14 offline test/vet；
- [x] 五平台 `CGO_ENABLED=0` cross-build；
- [x] TPDU/PDU 协商约束、20-item wire cap、failure-header 兼容和完整 span 已测试；
- [x] per-exchange timeout、streaming planner、fragment limit 和零值安全已测试；
- [x] CI 已配置 stable race/fuzz/coverage/govulncheck 与 Go 1.20.14 legacy matrix；
- [x] 新协议栈为唯一实现，旧 Handler/AG/PG/NCK/MPI/PPI/管理 API 已删除；
- [x] LICENSE/NOTICE/CHANGELOG/README 和多语言目录规范完成；
- [x] 默认测试不访问真实 PLC、公网或 native DLL；
- [x] qualification harness 有只读默认和精确 echo-write 门禁；
- [x] 完成 API/security/license 审计，已知限制写入 README 和本规范；
- [x] 临时审计文件、旧实现、旧测试、vendor PDF、截图和二进制资产不进入提交。

`v0.1.0` tag、release notes 和制品属于发布操作，不改变上述源码完成度。

### 16.4 延期且不阻塞 v0.1.0 源码提交

- 观察提交后的远端 CI/race 结果；
- 完成三类目标 PLC 的硬件资格矩阵；
- 完成 Windows 7 amd64 实机 smoke test；
- 验证真实断线恢复和 `WriteUnknown` reconciliation；
- 执行现场长稳、资源上限和多 PLC 并行测试。

这些项目没有测试证据时必须保持“未资格认证”的公开表述，不得反向推导为兼容承诺。

### 16.5 进入 v1.0 前必须完成

- 完成更完整的 PLC/固件矩阵和长稳测试；
- 固定公共 API 与错误/结果语义；
- 审核 Go 1.20/Windows 7 兼容线的长期安全维护方案；
- 评估 DATE/TIME/DTL 等 codec 是否属于稳定 API；
- 以实际设备证据决定是否需要 COTP fragmentation 或其他协议扩展；
- 完成真实业务 flow、升级和回滚验证。

## 17. 历史决策依据

本轮重构源于旧 fork 和主流 S7 驱动的实现对照。临时审计记录已在结论合入本规范
后删除，不构成仓库或发布内容。由此形成的最终取舍是：

- gos7 提供可靠的单 session、batch、PDU、ReadArea 和原生 bit；
- gos7 解析 TPDU/PDU、单 wire PDU 限制 20 item，并负责 large item/Area 分片；
- 地址位置和值类型分离，以支持 DBD 起点的 REAL/DWORD/LREAL；
- 范围合并、连接共享、日志和重连策略留在应用层；
- 旧应用读取 Handler.PDULength、自算 header 或自动 replay write 的行为不继承；
- 不引入 Snap7/native/JVM 依赖；scripted PLC 保持纯 Go 和离线；
- 不复制旧 runtime 代码，不为旧 API 调用方保留 compatibility wrapper。

此历史证据只解释设计来源；第 1 至第 16 节才是当前实现和发布规范。
