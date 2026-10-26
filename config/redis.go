package config

type Redis struct {
	DB       int    `mapstructure:"db" json:"db" yaml:"db"`                   // redis的哪个数据库
	Addr     string `mapstructure:"addr" json:"addr" yaml:"addr"`             // 服务器地址:端口
	Password string `mapstructure:"password" json:"password" yaml:"password"` // 密码

	// Dev 单独指定 Redis 连接所使用的网卡；留空表示跟随 system.dev，
	// 填 off / none / - 表示该连接不做网卡绑定（走系统默认路由）。
	Dev string `mapstructure:"dev" json:"dev" yaml:"dev"`
}
