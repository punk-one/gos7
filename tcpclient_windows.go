//go:build windows
// +build windows

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
	// 在Windows上，使用标准库的SetKeepAlive方法
	_ = tcpConn.SetKeepAlive(true)

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

	// 在Windows上，使用SyscallConn方法获取底层的socket句柄
	// 这是Go 1.11+引入的方法，可以在Windows上安全地获取socket句柄
	rawConn, err := tcpConn.SyscallConn()
	if err != nil {
		// 如果SyscallConn失败，只使用标准库的SetKeepAlive
		return
	}

	// 使用Control方法访问底层的socket句柄
	rawConn.Control(func(fd uintptr) {
		fdHandle := syscall.Handle(fd)
		// 设置 TCP_NODELAY 选项
		_ = syscall.SetsockoptInt(fdHandle, syscall.IPPROTO_TCP, syscall.TCP_NODELAY, 1)
		// 尝试设置TCP时间戳选项（如果支持）
		// 注意：TCP时间戳选项在某些Windows版本上可能不支持
		_ = syscall.Setsockopt(fdHandle, syscall.IPPROTO_TCP, 8, &options[4], 10)
	})
}
