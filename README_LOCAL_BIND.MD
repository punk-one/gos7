# gos7 扩展使用说明（支持绑定本地IP/端口）

本扩展在原 [gos7](https://github.com/robinson/gos7) 库的基础上，新增了 `ConnectWithLocal` 接口，允许用户在连接 PLC 时绑定本地网卡的 IP 和端口。这样可以在多网卡场景下控制每个连接走哪块网卡。

---

## 接口对比

| 方法 | 功能 | 使用场景 |
|------|------|----------|
| `Connect()` | 默认通过操作系统路由选择本地网卡建立连接 | 单网卡环境或无需指定网卡时 |
| `ConnectWithLocal(localAddr, localPort)` | 绑定指定本地 IP/端口建立连接 | 多网卡环境，需要精确控制使用的网卡 |

- **`localAddr`**: 指定本地网卡的 IP 地址（必须是本机已有网卡的 IP）。
- **`localPort`**: 指定本地端口。传 `0` 时系统会自动分配临时端口（推荐用法）。

---

## 使用示例

### 普通连接（旧用法）
```go
handler := gos7.NewTCPClientHandler("10.1.13.180", 0, 2)
client  := gos7.NewClient(handler)

err := handler.Connect()  // 由操作系统选择网卡
if err != nil {
    panic(err)
}
defer handler.Close()

// 使用 client 读写 PLC ...
```

### 指定网卡连接（新用法）
```go
handler := gos7.NewTCPClientHandler("10.1.13.180", 0, 2)
client  := gos7.NewClient(handler)

// 指定从本地网卡 10.1.13.201 发起连接，端口自动分配
err := handler.ConnectWithLocal("10.1.13.201", 0)
if err != nil {
    panic(err)
}
defer handler.Close()

// 使用 client 读写 PLC ...
```

### 多网卡连接多个 PLC
假设本机有 4 块网卡，分别是：
- LAN1: 10.1.13.201
- LAN2: 10.1.13.202
- LAN3: 10.1.13.203
- LAN4: 10.1.13.204

目标有 4 台 PLC，它们的 IP **相同**（10.1.13.180），但在物理上连接到不同交换机/网段。

```go
lanIPs := []string{"10.1.13.201", "10.1.13.202", "10.1.13.203", "10.1.13.204"}

for _, ip := range lanIPs {
    go func(localIP string) {
        handler := gos7.NewTCPClientHandler("10.1.13.180", 0, 2)
        client  := gos7.NewClient(handler)

        err := handler.ConnectWithLocal(localIP, 0) // 每个连接绑定不同网卡
        if err != nil {
            fmt.Printf("[%s] connect failed: %v\\n", localIP, err)
            return
        }
        defer handler.Close()

        fmt.Printf("Connected to PLC from %s\\n", localIP)
        // 使用 client 采集数据 ...

    }(ip)
}

// 等待所有 goroutine 完成 ...
```

---

## 拓扑示意图（PNG）

![多网卡绑定多个PLC示意图]https://raw.githubusercontent.com/punk-one/gos7/master/local_bind.png)

> 图示：本机 4 个网卡分别连接到 4 台 PLC，每台 PLC 的 IP 相同 (10.1.13.180)，但通过物理隔离的网络实现并行访问。
