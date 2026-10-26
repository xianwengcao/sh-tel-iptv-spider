package config

type System struct {
	Env     string `mapstructure:"env" json:"env" yaml:"env"`            // 环境值
	Addr    string `mapstructure:"addr" json:"addr" yaml:"addr"`         // 端口值
	DbType  string `mapstructure:"db-type" json:"dbType" yaml:"db-type"` // 数据库类型:mysql(默认)|sqlite|sqlserver|postgresql
	OSSType string `mapstructure:"oss-type" json:"ossType" yaml:"oss-type"`

	// Dev 出站网卡绑定：让程序所有对外通信都从指定的网络接口发出。
	// 取值：网卡名（eth0 / pppoe-wan / br-lan / eth0.85 ...）或本机 IPv4 地址；
	// 留空表示不绑定（保持原有走系统路由表的行为）。
	Dev string `mapstructure:"dev" json:"dev" yaml:"dev"`

	// DevDNS 是否把 DNS 解析也绑定到 Dev 指定的网卡。
	// 默认 false：专网网卡上通常没有 DNS，强制绑定会导致域名解析失败。
	DevDNS bool `mapstructure:"dev-dns" json:"devDns" yaml:"dev-dns"`
}
