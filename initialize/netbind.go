package initialize

import (
	"fmt"
	"strings"

	"iptv-spider-sh/global"
	"iptv-spider-sh/modules/netbind"
)

// NetBind 依据配置文件初始化「出站网卡绑定」。
//
// system.dev 是总开关：填写网卡名（eth0 / pppoe-wan / br-lan / eth0.85 ...）
// 或该网卡上的本机 IPv4 地址后，程序所有对外通信
// （IPTV 认证 / EPG / 频道列表、MySQL、Redis、对象存储）都会从该网卡发出。
//
// 三个服务可以单独覆盖，取值见 mysql.dev / redis.dev / oss.dev：
//   - 留空            => 跟随 system.dev
//   - off / none / -  => 该连接不做绑定，走系统默认路由
//     （数据库、缓存服务器在局域网时通常应该关掉，否则会因为出口网卡不对而连不上）
func NetBind() netbind.Group {
	sys := global.CONFIG.System
	bindDNS := sys.DevDNS

	def, err := netbind.New(netbind.Options{
		Dev:     sys.Dev,
		BindDNS: bindDNS,
		Logger:  global.LOG,
	})
	if err != nil {
		bindLogError("出站网卡绑定初始化失败，本次不启用：" + err.Error())
		return netbind.Group{
			Default: netbind.Disabled(),
			MySQL:   netbind.Disabled(),
			Redis:   netbind.Disabled(),
			OSS:     netbind.Disabled(),
		}
	}

	g := netbind.Group{
		Default: def,
		MySQL:   bindPick("mysql", def, global.CONFIG.Mysql.Dev, bindDNS),
		Redis:   bindPick("redis", def, global.CONFIG.Redis.Dev, bindDNS),
		OSS:     bindPick("oss", def, global.CONFIG.OSS.Dev, bindDNS),
	}

	// HTTP：兜底替换进程级默认传输，覆盖所有直接使用 http.DefaultClient /
	// http.DefaultTransport 的库（如 COS 的签名传输层）。
	// resty 客户端会在 modules/http_client 里显式使用 Default 的 Transport。
	g.Default.ApplyHTTPDefault()
	// MySQL：必须在 gorm.Open 之前注册自定义拨号，否则不生效。
	g.MySQL.ApplyMySQLDialer()

	if def.Enabled() {
		bindLogInfo("出站网卡绑定已启用：" + def.Describe())
		bindLogInfo(fmt.Sprintf("  ├─ IPTV/HTTP : %s", g.Default.Describe()))
		bindLogInfo(fmt.Sprintf("  ├─ MySQL     : %s", g.MySQL.Describe()))
		bindLogInfo(fmt.Sprintf("  ├─ Redis     : %s", g.Redis.Describe()))
		bindLogInfo(fmt.Sprintf("  └─ 对象存储  : %s", g.OSS.Describe()))
	} else if strings.TrimSpace(sys.Dev) != "" {
		bindLogInfo("system.dev 解析失败，出站网卡绑定未启用")
	} else {
		bindLogInfo("出站网卡绑定未启用（system.dev 为空，走系统默认路由）")
	}
	return g
}

// bindPick 解析单个服务的 dev 覆盖值：
// 留空跟随全局；off/none/- 关闭；其它值单独解析。
func bindPick(name string, def *netbind.Binder, override string, bindDNS bool) *netbind.Binder {
	override = strings.TrimSpace(override)
	if override == "" {
		return def
	}
	if netbind.IsOff(override) {
		bindLogInfo(fmt.Sprintf("%s.dev=%s，该连接不做网卡绑定，走系统默认路由", name, override))
		return netbind.Disabled()
	}
	b, err := netbind.New(netbind.Options{Dev: override, BindDNS: bindDNS, Logger: global.LOG})
	if err != nil {
		bindLogError(fmt.Sprintf("%s.dev=%s 解析失败，回退为跟随 system.dev：%v", name, override, err))
		return def
	}
	return b
}

func bindLogInfo(msg string) {
	if global.LOG != nil {
		global.LOG.Info(msg)
	} else {
		fmt.Println("[netbind] " + msg)
	}
}

func bindLogError(msg string) {
	if global.LOG != nil {
		global.LOG.Error(msg)
	} else {
		fmt.Println("[netbind] " + msg)
	}
}
