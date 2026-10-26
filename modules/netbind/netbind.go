// Package netbind 把本进程的「出站」通信绑定到指定的网络接口（网卡）。
//
// 为什么需要它：
//
//	上海电信 IPTV 的认证、EPG、频道列表等接口都在运营商专网里，程序所在主机
//	往往有多张网卡（上网口 + IPTV 专网口）。只依赖路由表选择出口并不总是可靠，
//	而一旦请求从错误的网卡出去，认证就会失败。
//
// 绑定方式（两种同时使用，互为补充）：
//
//  1. 源地址绑定：把新连接的本地源地址固定为该网卡上的 IPv4 地址。全平台通用。
//  2. 网卡绑定：Linux 下额外对新 socket 设置 SO_BINDTODEVICE，强制报文从该网卡
//     发出（需要 root / CAP_NET_RAW；失败时自动降级为仅源地址绑定，不阻断连接）。
//
// dev 可以填网卡名（eth0 / pppoe-wan / br-lan / eth0.85 ...），也可以直接填该网卡
// 上的本机 IPv4 地址。留空或填 off / none / - 表示不启用，程序保持原有行为。
package netbind

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	dialTimeout = 30 * time.Second
	keepAlive   = 30 * time.Second
	dnsTimeout  = 10 * time.Second
)

// offWords 表示“该服务不做网卡绑定”。
var offWords = map[string]struct{}{
	"off": {}, "none": {}, "no": {}, "false": {}, "disable": {}, "disabled": {}, "-": {}, "0": {},
}

// IsOff 判断配置值是否表示“关闭绑定”。
func IsOff(v string) bool {
	_, ok := offWords[strings.ToLower(strings.TrimSpace(v))]
	return ok
}

// Options 创建 Binder 的参数。
type Options struct {
	// Dev 网卡名或本机 IPv4 地址；留空 = 不绑定。
	Dev string
	// BindDNS 是否连 DNS 解析也走该网卡。
	BindDNS bool
	// Logger 可为 nil。
	Logger *zap.Logger
}

// Binder 描述一次「出站通信绑定到某个网卡」的配置。
// 零值（以及 nil *Binder）表示未启用，所有方法都是安全的空实现。
type Binder struct {
	dev     string // 配置里的原始取值
	ifName  string // 网卡名
	ifIndex int    // 网卡索引
	localIP net.IP // 绑定用的源地址（网卡上的 IPv4）
	bindDNS bool
	log     *zap.Logger

	enabled bool

	devWarnOnce sync.Once // SO_BINDTODEVICE 失败只告警一次
	transOnce   sync.Once
	transport   *http.Transport
	resolver    *net.Resolver
}

// New 依据 Options 解析并创建 Binder。
// dev 为空或 off 时返回一个未启用的 Binder 且不报错。
func New(o Options) (*Binder, error) {
	b := &Binder{log: o.Logger, bindDNS: o.BindDNS}
	dev := strings.TrimSpace(o.Dev)
	if dev == "" || IsOff(dev) {
		return b, nil
	}
	if err := b.resolve(dev); err != nil {
		return nil, err
	}
	b.dev = dev
	b.enabled = true
	if b.bindDNS {
		b.resolver = b.newBoundResolver()
	}
	return b, nil
}

// Disabled 返回一个不绑定任何网卡的 Binder。
func Disabled() *Binder { return &Binder{} }

// Enabled 报告是否启用了网卡绑定。对 nil 接收者安全。
func (b *Binder) Enabled() bool { return b != nil && b.enabled }

// Dev 返回配置里的原始取值。
func (b *Binder) Dev() string {
	if b == nil {
		return ""
	}
	return b.dev
}

// LocalIP 返回绑定使用的源地址，未启用或网卡无 IPv4 时返回 nil。
func (b *Binder) LocalIP() net.IP {
	if b == nil {
		return nil
	}
	return b.localIP
}

// Interface 返回解析出的网卡名，未启用时为空。
func (b *Binder) Interface() string {
	if b == nil {
		return ""
	}
	return b.ifName
}

// Describe 返回可直接打日志的描述文本。
func (b *Binder) Describe() string {
	if !b.Enabled() {
		return "未启用（走系统默认路由）"
	}
	parts := []string{"dev=" + b.dev}
	if b.ifName != "" {
		parts = append(parts, fmt.Sprintf("网卡=%s(ifindex=%d)", b.ifName, b.ifIndex))
	}
	if b.localIP != nil {
		parts = append(parts, "源地址="+b.localIP.String())
	} else {
		parts = append(parts, "源地址=未取到IPv4（仅能依赖网卡绑定）")
	}
	if b.bindDNS {
		parts = append(parts, "DNS=同网卡")
	}
	return strings.Join(parts, ", ")
}

// ---------------------------------------------------------------------------
// 解析
// ---------------------------------------------------------------------------

func (b *Binder) resolve(dev string) error {
	// 1) dev 直接就是本机 IP
	if ip := net.ParseIP(dev); ip != nil {
		v4 := ip.To4()
		if v4 == nil {
			return fmt.Errorf("dev=%q 不是 IPv4 地址（本功能仅支持 IPv4）", dev)
		}
		b.localIP = v4
		if ifi := interfaceByIP(v4); ifi != nil {
			b.ifName, b.ifIndex = ifi.Name, ifi.Index
		} else {
			b.logf("warn", "dev=%s 是本机 IP，但没有找到持有该地址的网卡，将只做源地址绑定", dev)
		}
		return nil
	}

	// 2) dev 是网卡名
	ifi, err := net.InterfaceByName(dev)
	if err != nil {
		return fmt.Errorf("找不到网卡 %q（也可直接填写该网卡的本机 IPv4 地址）: %w", dev, err)
	}
	b.ifName, b.ifIndex = ifi.Name, ifi.Index
	if ip := firstIPv4(ifi); ip != nil {
		b.localIP = ip
	} else {
		b.logf("warn", "网卡 %s 上没有 IPv4 地址，将只依赖网卡绑定（SO_BINDTODEVICE）", ifi.Name)
	}
	return nil
}

// firstIPv4 取网卡上的第一个 IPv4 地址。
func firstIPv4(ifi *net.Interface) net.IP {
	if ifi == nil {
		return nil
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if v4 := ip.To4(); v4 != nil {
			return v4
		}
	}
	return nil
}

// interfaceByIP 找到持有指定 IPv4 的网卡。
func interfaceByIP(ip net.IP) *net.Interface {
	ifis, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for i := range ifis {
		ifi := ifis[i]
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if ipnet.IP.Equal(ip) {
				return &ifi
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 拨号
// ---------------------------------------------------------------------------

// DialContext 供 net.Dialer 风格（带 context）的调用方使用。
// 未启用时等价于默认拨号，行为与改动前完全一致。
func (b *Binder) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if !b.Enabled() {
		return (&net.Dialer{Timeout: dialTimeout, KeepAlive: keepAlive}).DialContext(ctx, network, address)
	}
	// b.resolver 只在 system.dev-dns=true 时非空，用于让 DNS 解析也走该网卡
	return b.dialerFor(network, b.resolver).DialContext(ctx, network, address)
}

// DialNet 供只接受 (network, addr) 签名的第三方库使用，例如 redigo 的 DialNetDial。
func (b *Binder) DialNet(network, addr string) (net.Conn, error) {
	return b.DialContext(context.Background(), network, addr)
}

// dialerFor 构造一个绑定到目标网卡的 net.Dialer。
// network 用来决定 LocalAddr 的类型（*net.TCPAddr / *net.UDPAddr）。
func (b *Binder) dialerFor(network string, resolver *net.Resolver) *net.Dialer {
	d := &net.Dialer{Timeout: dialTimeout, KeepAlive: keepAlive, Resolver: resolver}
	if local := b.localAddr(network); local != nil {
		d.LocalAddr = local
	}
	if b.ifName != "" {
		d.Control = b.control
	}
	return d
}

func (b *Binder) localAddr(network string) net.Addr {
	if b.localIP == nil {
		return nil
	}
	switch {
	case strings.HasPrefix(network, "tcp"):
		return &net.TCPAddr{IP: b.localIP}
	case strings.HasPrefix(network, "udp"):
		return &net.UDPAddr{IP: b.localIP}
	}
	return nil
}

// newBoundResolver 让 DNS 解析也走指定网卡（由 system.dev-dns 控制，默认关闭）。
func (b *Binder) newBoundResolver() *net.Resolver {
	return &net.Resolver{
		PreferGo:     true,
		StrictErrors: false,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := &net.Dialer{Timeout: dnsTimeout}
			if local := b.localAddr(network); local != nil {
				d.LocalAddr = local
			}
			if b.ifName != "" {
				d.Control = b.control
			}
			return d.DialContext(ctx, network, address)
		},
	}
}

// ---------------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------------

// Transport 返回一个把出站连接绑定到该网卡的 *http.Transport，
// 可直接用于 resty / minio / COS 等支持自定义 RoundTripper 的库，
// 也可赋给 http.DefaultTransport。
//
// 未启用时返回 nil，表示“沿用调用方自己的默认传输”，
// 这样关闭功能时程序行为与改动前完全一致。
func (b *Binder) Transport() http.RoundTripper {
	if !b.Enabled() {
		return nil
	}
	b.transOnce.Do(func() {
		var t *http.Transport
		if base, ok := http.DefaultTransport.(*http.Transport); ok {
			t = base.Clone()
		} else {
			t = &http.Transport{Proxy: http.ProxyFromEnvironment}
		}
		t.DialContext = b.DialContext
		if t.Proxy == nil {
			t.Proxy = http.ProxyFromEnvironment
		}
		b.transport = t
	})
	return b.transport
}

// ApplyHTTPDefault 替换进程级的 http.DefaultTransport，
// 使所有使用 http.DefaultClient / http.DefaultTransport 的第三方库也走绑定网卡。
func (b *Binder) ApplyHTTPDefault() {
	if !b.Enabled() {
		return
	}
	http.DefaultTransport = b.Transport()
}

// ---------------------------------------------------------------------------
// Group
// ---------------------------------------------------------------------------

// Group 汇总各服务使用的 Binder。未单独配置的服务共用 Default。
type Group struct {
	Default *Binder // system.dev：IPTV 抓取等 HTTP 通信
	MySQL   *Binder // mysql.dev，留空跟随 Default
	Redis   *Binder // redis.dev，留空跟随 Default
	OSS     *Binder // oss.dev，留空跟随 Default
}

// ---------------------------------------------------------------------------
// 日志辅助
// ---------------------------------------------------------------------------

func (b *Binder) logf(level, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if b != nil && b.log != nil {
		switch level {
		case "error":
			b.log.Error(msg)
		case "warn":
			b.log.Warn(msg)
		default:
			b.log.Info(msg)
		}
		return
	}
	fmt.Printf("[netbind] %s\n", msg)
}

// warnBindDeviceOnce SO_BINDTODEVICE 失败时只告警一次，避免刷屏。
func (b *Binder) warnBindDeviceOnce(err error) {
	b.devWarnOnce.Do(func() {
		b.logf("warn",
			"SO_BINDTODEVICE(%s) 设置失败: %v；已降级为仅按源地址 %s 绑定（如需强制网卡绑定，请以 root 运行）",
			b.ifName, err, b.localIP)
	})
}
