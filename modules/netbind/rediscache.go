package netbind

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/astaxie/beego/cache"
	"github.com/gomodule/redigo/redis"
)

// DefaultRedisKey 与 beego 自带 redis 缓存适配器的默认 key 前缀保持一致，
// 这样启用/停用本功能时缓存数据可以互相复用。
const DefaultRedisKey = "beecacheRedis"

// RedisCache 与 beego 自带的 redis 缓存适配器行为一致，
// 区别只有一处：连接的建立方式由 Binder 接管，可以从指定网卡出去。
//
// 之所以要自己实现一份，是因为 beego 的适配器内部直接调用 redis.Dial，
// 没有暴露任何自定义拨号的入口，无法对它做网卡绑定。
type RedisCache struct {
	pool     *redis.Pool
	conninfo string
	dbNum    int
	key      string
	password string
	maxIdle  int
	timeout  time.Duration

	binder *Binder
}

// NewBoundRedisCache 创建一个走指定网卡的 redis 缓存适配器。
// 用法与 beego 的一致：NewBoundRedisCache(binder).StartAndGC(configJSON)
func NewBoundRedisCache(b *Binder) *RedisCache {
	return &RedisCache{binder: b, key: DefaultRedisKey}
}

var _ cache.Cache = (*RedisCache)(nil)

// Get 读取缓存。
func (rc *RedisCache) Get(key string) interface{} {
	if v, err := rc.do("GET", key); err == nil {
		return v
	}
	return nil
}

// GetMulti 批量读取缓存。
func (rc *RedisCache) GetMulti(keys []string) []interface{} {
	c := rc.pool.Get()
	defer c.Close()
	args := make([]interface{}, 0, len(keys))
	for _, key := range keys {
		args = append(args, rc.associate(key))
	}
	values, err := redis.Values(c.Do("MGET", args...))
	if err != nil {
		return nil
	}
	return values
}

// Put 写入缓存并设置过期时间。
func (rc *RedisCache) Put(key string, val interface{}, timeout time.Duration) error {
	_, err := rc.do("SETEX", key, int64(timeout/time.Second), val)
	return err
}

// Delete 删除缓存。
func (rc *RedisCache) Delete(key string) error {
	_, err := rc.do("DEL", key)
	return err
}

// IsExist 判断缓存是否存在。
func (rc *RedisCache) IsExist(key string) bool {
	v, err := redis.Bool(rc.do("EXISTS", key))
	if err != nil {
		return false
	}
	return v
}

// Incr 计数器自增。
func (rc *RedisCache) Incr(key string) error {
	_, err := redis.Bool(rc.do("INCRBY", key, 1))
	return err
}

// Decr 计数器自减。
func (rc *RedisCache) Decr(key string) error {
	_, err := redis.Bool(rc.do("INCRBY", key, -1))
	return err
}

// ClearAll 清空本前缀下的全部缓存。
func (rc *RedisCache) ClearAll() error {
	cachedKeys, err := rc.Scan(rc.key + ":*")
	if err != nil {
		return err
	}
	c := rc.pool.Get()
	defer c.Close()
	for _, str := range cachedKeys {
		if _, err = c.Do("DEL", str); err != nil {
			return err
		}
	}
	return err
}

// Scan 按 pattern 扫描 key。
func (rc *RedisCache) Scan(pattern string) (keys []string, err error) {
	c := rc.pool.Get()
	defer c.Close()
	var (
		cursor uint64 = 0
		result []interface{}
		list   []string
	)
	for {
		result, err = redis.Values(c.Do("SCAN", cursor, "MATCH", pattern, "COUNT", 1024))
		if err != nil {
			return
		}
		list, err = redis.Strings(result[1], nil)
		if err != nil {
			return
		}
		keys = append(keys, list...)
		cursor, err = redis.Uint64(result[0], nil)
		if err != nil {
			return
		}
		if cursor == 0 {
			return
		}
	}
}

// StartAndGC 解析配置并建立连接池。
// config 形如 {"key":"collection key","conn":"connection info","dbNum":"0"}
func (rc *RedisCache) StartAndGC(config string) error {
	cf := map[string]string{}
	if err := json.Unmarshal([]byte(config), &cf); err != nil {
		return fmt.Errorf("redis 缓存配置解析失败: %w", err)
	}
	if _, ok := cf["key"]; !ok {
		cf["key"] = DefaultRedisKey
	}
	if _, ok := cf["conn"]; !ok {
		return errors.New("config has no conn key")
	}

	// 兼容 redis://<password>@<host>:<port> 写法
	cf["conn"] = strings.Replace(cf["conn"], "redis://", "", 1)
	if i := strings.Index(cf["conn"], "@"); i > -1 {
		cf["password"] = cf["conn"][0:i]
		cf["conn"] = cf["conn"][i+1:]
	}
	if _, ok := cf["dbNum"]; !ok {
		cf["dbNum"] = "0"
	}
	if _, ok := cf["password"]; !ok {
		cf["password"] = ""
	}
	if _, ok := cf["maxIdle"]; !ok {
		cf["maxIdle"] = "3"
	}
	if _, ok := cf["timeout"]; !ok {
		cf["timeout"] = "180s"
	}

	rc.key = cf["key"]
	rc.conninfo = cf["conn"]
	rc.dbNum, _ = strconv.Atoi(cf["dbNum"])
	rc.password = cf["password"]
	rc.maxIdle, _ = strconv.Atoi(cf["maxIdle"])
	if v, err := time.ParseDuration(cf["timeout"]); err == nil {
		rc.timeout = v
	} else {
		rc.timeout = 180 * time.Second
	}

	rc.connectInit()

	c := rc.pool.Get()
	defer c.Close()
	return c.Err()
}

func (rc *RedisCache) connectInit() {
	rc.pool = &redis.Pool{
		MaxIdle:     rc.maxIdle,
		IdleTimeout: rc.timeout,
		Dial: func() (redis.Conn, error) {
			// 与 beego 的唯一差异：拨号走绑定网卡
			c, err := redis.Dial("tcp", rc.conninfo, redis.DialNetDial(rc.binder.DialNet))
			if err != nil {
				return nil, err
			}
			if rc.password != "" {
				if _, err := c.Do("AUTH", rc.password); err != nil {
					c.Close()
					return nil, err
				}
			}
			if _, err := c.Do("SELECT", rc.dbNum); err != nil {
				c.Close()
				return nil, err
			}
			return c, nil
		},
	}
}

// do 执行一条 redis 命令，args[0] 必须是 key。
func (rc *RedisCache) do(commandName string, args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return nil, errors.New("missing required arguments")
	}
	args[0] = rc.associate(args[0])
	c := rc.pool.Get()
	defer c.Close()
	return c.Do(commandName, args...)
}

// associate 给 key 加上配置的前缀，保持与 beego 一致的存储结构。
func (rc *RedisCache) associate(originKey interface{}) string {
	return fmt.Sprintf("%s:%s", rc.key, originKey)
}
