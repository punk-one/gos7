//go:build !windows
// +build !windows

package gos7

// Copyright 2018 Trung Hieu Le. All rights reserved.
// This software may be modified and distributed under the terms
// of the BSD license. See the LICENSE file for details.
import (
	"net"
	"syscall"
	"time"
)

func setTCPOptions(tcpConn *net.TCPConn) {
	// 创建TCP选项数据
	options := []byte{
		0x01,       // NOP
		0x01,       // NOP
		0x08, 0x0a, // 时间戳选项长度 + 类型
		0x00, 0x00, 0x00, 0x00, // 时间戳值预留空间
		0x00, 0x00, 0x00, 0x00, // 回声时间戳值预留空间
	}
	// 获取当前时间戳
	now := time.Now().UnixNano() / 1000000 // 转换成毫秒
	timestamp := uint32(now)
	// 填充时间戳值
	copy(options[4:], []byte{
		byte(timestamp >> 24),
		byte(timestamp >> 16),
		byte(timestamp >> 8),
		byte(timestamp),
	})
	// 设置TCP选项
	// 获取连接的文件描述符
	fd, err := tcpConn.File()
	if err != nil {
		return
	}
	defer fd.Close()
	// 在 Unix 系统上，fd.Fd() 返回 int
	fdInt := int(fd.Fd())
	// 设置 TCP 保活选项 (SO_KEEPALIVE)
	_ = syscall.SetsockoptInt(fdInt, syscall.SOL_SOCKET, syscall.SO_KEEPALIVE, 1)
	// 注意：TCP 时间戳选项在 Unix 系统上通常需要更底层的操作
	// 这里简化处理，只设置基本的 keepalive
}

