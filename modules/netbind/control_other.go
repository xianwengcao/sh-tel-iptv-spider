//go:build !linux

package netbind

import "syscall"

// control 在非 Linux 平台（Windows / macOS / BSD）上是空操作。
// 这些系统没有 SO_BINDTODEVICE，绑定只能通过“源地址 = 网卡 IPv4”实现，
// 因此在非 Linux 平台上 dev 建议直接填写本机 IPv4 地址。
func (b *Binder) control(network, address string, c syscall.RawConn) error {
	return nil
}
