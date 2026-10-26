//go:build linux

package netbind

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// control 在每个新建的 socket 上设置 SO_BINDTODEVICE，
// 强制该 socket 的报文一定从指定网卡发出（比仅绑定源地址更彻底）。
//
// 需要 root 或 CAP_NET_RAW 权限。失败时不阻断连接：
// 记录一次告警后降级为“仅按源地址绑定”，功能仍然可用。
func (b *Binder) control(network, address string, c syscall.RawConn) error {
	if b.ifName == "" {
		return nil
	}
	var setErr error
	if err := c.Control(func(fd uintptr) {
		setErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, b.ifName)
	}); err != nil {
		return err
	}
	if setErr != nil {
		b.warnBindDeviceOnce(setErr)
	}
	return nil
}
