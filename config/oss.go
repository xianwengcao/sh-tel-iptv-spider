package config

type OSS struct {
	Enable     bool   `mapstructure:"enable" json:"enable" yaml:"enable"`
	UploadCron string `mapstructure:"upload_cron" json:"upload_cron" yaml:"upload_cron"`
	EndPoint   string `mapstructure:"endpoint" json:"endpoint" yaml:"endpoint"`
	UseSSL     bool   `mapstructure:"use-ssl" json:"use-ssl" yaml:"use-ssl"`
	Bucket     string `mapstructure:"bucket" json:"bucket" yaml:"bucket"`
	AccessKey  string `mapstructure:"access-key" json:"access-key" yaml:"access-key"`
	SecretKey  string `mapstructure:"secret-key" json:"secret-key" yaml:"secret-key"`

	// Dev 单独指定对象存储(Minio/COS)所使用的网卡；留空表示跟随 system.dev，
	// 填 off / none / - 表示该连接不做网卡绑定（走系统默认路由）。
	Dev string `mapstructure:"dev" json:"dev" yaml:"dev"`
}
