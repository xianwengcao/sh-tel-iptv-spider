package netbind

import (
	"context"
	"net"

	"github.com/go-sql-driver/mysql"
)

// ApplyMySQLDialer 把 go-sql-driver/mysql 的 tcp 拨号替换为绑定拨号，
// 从而让 gorm/MySQL 的连接也从指定网卡出去。
//
// 注意：必须在 gorm.Open 之前调用才有效（驱动在建立连接时才取这个拨号函数）。
func (b *Binder) ApplyMySQLDialer() {
	if !b.Enabled() {
		return
	}
	mysql.RegisterDialContext("tcp", func(ctx context.Context, addr string) (net.Conn, error) {
		return b.DialContext(ctx, "tcp", addr)
	})
	// go-sql-driver 也支持 unix socket（网络名 unix），这里不做处理：
	// unix socket 不经过网卡。
}
