package zeroconf

import (
	"syscall"
)

// Linux 系统上的 SO_REUSEPORT 常量定义
const SO_REUSEPORT = 15

// setReusePort 在Linux系统上设置端口复用选项
func setReusePort(c syscall.RawConn) error {
	var opErr error
	err := c.Control(func(fd uintptr) {
		socketFd := int(fd)
		// 设置 SO_REUSEADDR 选项
		opErr = syscall.SetsockoptInt(socketFd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
		if opErr != nil {
			return
		}

		// 设置 SO_REUSEPORT 选项（Linux系统支持）
		opErr = syscall.SetsockoptInt(socketFd, syscall.SOL_SOCKET, SO_REUSEPORT, 1)
		if opErr != nil {
			// 如果SO_REUSEPORT失败，在某些系统上可能仍然可以工作，所以不返回错误
			// 只有SO_REUSEADDR是必需的
			opErr = nil
		}
	})
	if err != nil {
		return err
	}
	return opErr
}
