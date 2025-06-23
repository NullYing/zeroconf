package zeroconf

import (
	"syscall"
)

// setReusePort 在Windows系统上设置端口复用选项
func setReusePort(c syscall.RawConn) error {
	var opErr error
	err := c.Control(func(fd uintptr) {
		// Windows 系统处理 - 转换为 Handle 类型
		handle := syscall.Handle(fd)
		// 只设置 SO_REUSEADDR 选项，Windows 不支持 SO_REUSEPORT
		opErr = syscall.SetsockoptInt(handle, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	})
	if err != nil {
		return err
	}
	return opErr
}
