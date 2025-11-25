//go:build windows
// +build windows

package gos7

// Copyright 2018 Trung Hieu Le. All rights reserved.
// This software may be modified and distributed under the terms
// of the BSD license. See the LICENSE file for details.
import (
	"fmt"
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
		fmt.Println("Error getting file:", err)
		return
	}
	defer fd.Close()
	// 使用 uintptr 转换为 Windows Handle
	fdHandle := syscall.Handle(fd.Fd())
	// 设置 TCP 保活选项
	syscall.SetsockoptInt(fdHandle, syscall.IPPROTO_TCP, 1, 1) // 设置开始 Keepalive 的时间
	syscall.SetsockoptInt(fdHandle, syscall.IPPROTO_TCP, 1, 1) // 设置开始 Keepalive 的时间
	syscall.Setsockopt(fdHandle, syscall.IPPROTO_TCP, 8, &options[4], 10)
	syscall.Setsockopt(fdHandle, syscall.SOCK_RAW, 8, &options[4], 10)
}
