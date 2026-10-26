package initialize

import (
	"fmt"

	"github.com/astaxie/beego/cache"
	_ "github.com/astaxie/beego/cache/redis"
	"iptv-spider-sh/global"
	"iptv-spider-sh/modules/netbind"
)

func Cache() cache.Cache {
	t := global.CONFIG.Cache.Type
	var c string
	switch t {
	case "redis":
		c = fmt.Sprintf(`{"key":"%s","conn":"%s","dbNum":"%d","password":"%s"}`,
			global.CONFIG.Cache.Prefix,
			global.CONFIG.Redis.Addr,
			global.CONFIG.Redis.DB,
			global.CONFIG.Redis.Password)
		// Redis 走指定网卡：beego 自带的 redis 适配器没有自定义拨号的入口，
		// 因此在启用网卡绑定时改用行为一致的绑定版本（key 前缀、存储结构完全相同）。
		if binder := global.BIND.Redis; binder.Enabled() {
			rc := netbind.NewBoundRedisCache(binder)
			if err := rc.StartAndGC(c); err != nil {
				panic(err)
			}
			return rc
		}
	case "memory":
		fallthrough
	default:
		t = "memory"
		memoryInterval := global.CONFIG.Cache.Interval
		if memoryInterval == 0 {
			memoryInterval = 60
		}
		c = fmt.Sprintf(`{"interval": %d}`, memoryInterval)
	}
	bm, err := cache.NewCache(t, c)
	if err != nil {
		panic(err)
	}
	if bm == nil {
		panic("initialize failed, Bm is nil!")
	}
	return bm
}
