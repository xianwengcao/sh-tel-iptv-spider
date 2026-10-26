// 出站网卡连通性自检。
//
// 用途：`iptv-spider-sh -netcheck` 会把本机所有出口（配置的 system.dev、系统默认
// 路由、以及其余每张网卡）逐一去连 IPTV 认证服务器，直接告诉你「哪张网卡能连通专网」。
//
// 之所以需要它：一旦 system.dev 指到了到不了专网的网卡（例如指到了 br-lan，或者
// 该网卡上根本没有可用 IPv4 / 路由），认证请求只会报一句
// `dial tcp x.x.x.x:7001: i/o timeout`，看不出到底是网卡选错了还是专网不通。
package netbind

import (
	"context"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"time"
)

// ProbeResult 描述一次从指定出口到目标地址的 TCP 连通性探测结果。
type ProbeResult struct {
	Label   string        // 出口的可读描述
	Dev     string        // 网卡名；空表示走系统默认路由
	Source  string        // 探测时使用的源地址（"自动" 表示交给内核选择）
	Target  string        // 目标 host:port
	OK      bool          // 是否连通
	Elapsed time.Duration // 耗时
	Err     error         // 失败原因
}

// Probe 从本 Binder 描述的出口对 target 做一次 TCP 连接测试。
// Binder 未启用时走系统默认路由，可用来和绑定后的结果做对照。
func (b *Binder) Probe(ctx context.Context, target string, timeout time.Duration) ProbeResult {
	res := ProbeResult{Target: target, Source: "自动"}
	if b.Enabled() {
		res.Dev = b.dev
		res.Label = "dev=" + b.dev
		if b.ifName != "" {
			res.Dev = b.ifName
			res.Label = fmt.Sprintf("dev=%s (%s)", b.dev, b.ifName)
		}
		if b.localIP != nil {
			res.Source = b.localIP.String()
		}
	} else {
		res.Label = "default-route"
	}
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	conn, err := b.DialContext(cctx, "tcp", target)
	res.Elapsed = time.Since(start)
	if err != nil {
		res.Err = err
		return res
	}
	_ = conn.Close()
	res.OK = true
	return res
}

// InterfaceInfo 描述一张可用于出站的网卡。
type InterfaceInfo struct {
	Name  string
	Index int
	Up    bool
	IPv4  []string
}

// ListInterfaces 列出本机所有非 loopback 网卡（含没有 IPv4 的），按 ifindex 排序。
func ListInterfaces() []InterfaceInfo {
	ifis, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := make([]InterfaceInfo, 0, len(ifis))
	for _, ifi := range ifis {
		if ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		info := InterfaceInfo{
			Name:  ifi.Name,
			Index: ifi.Index,
			Up:    ifi.Flags&net.FlagUp != 0,
		}
		if addrs, err := ifi.Addrs(); err == nil {
			for _, a := range addrs {
				var ip net.IP
				switch v := a.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				}
				if v4 := ip.To4(); v4 != nil {
					info.IPv4 = append(info.IPv4, v4.String())
				}
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out
}

type exitCandidate struct {
	mark  string // "*" = 配置里 system.dev 指定的出口
	label string // 纯 ASCII，保证列对齐
	b     *Binder
}

// Diagnose 打印「本机的哪个出口能到达目标地址」的诊断报告。
//
// dev 为配置里 system.dev 的取值，仅用于在报告中标注哪一条是当前生效的出口。
// 返回 true 表示至少有一个出口能连通至少一个目标。
func Diagnose(ctx context.Context, dev string, targets []string, timeout time.Duration, w io.Writer) bool {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	line := func(format string, a ...interface{}) {
		fmt.Fprintf(w, format+"\n", a...)
	}

	line("=================== 出站网卡连通性自检 ===================")
	line("目标地址最长等待时间：%s", timeout)
	line("")

	ifis := ListInterfaces()
	line("本机网卡：")
	if len(ifis) == 0 {
		line("  （没有找到非 loopback 网卡）")
	}
	for _, i := range ifis {
		state := "DOWN"
		if i.Up {
			state = "UP  "
		}
		addrs := strings.Join(i.IPv4, ",")
		if addrs == "" {
			addrs = "无 IPv4"
		}
		line("  %-16s index=%-4d %s  ipv4=%s", i.Name, i.Index, state, addrs)
	}
	line("")

	// 候进出口：配置的 dev（标记 *）→ 系统默认路由 → 其余每张 UP 的网卡
	var exits []exitCandidate
	seen := map[string]bool{}
	cfgDev := strings.TrimSpace(dev)
	if cfgDev != "" && !IsOff(cfgDev) {
		if b, err := New(Options{Dev: cfgDev}); err != nil {
			line("⚠ 配置的 system.dev=%q 解析失败：%v", cfgDev, err)
			line("  （程序启动时也不会启用网卡绑定，等同未绑定）")
			line("")
		} else {
			exits = append(exits, exitCandidate{mark: "*", label: "dev=" + cfgDev, b: b})
			if b.ifName != "" {
				seen[b.ifName] = true
			}
		}
	} else {
		line("ℹ 配置里 system.dev 为空，当前程序不做网卡绑定（走系统默认路由）")
		line("")
	}
	exits = append(exits, exitCandidate{mark: " ", label: "default-route", b: Disabled()})
	for _, i := range ifis {
		if !i.Up || seen[i.Name] {
			continue
		}
		b, err := New(Options{Dev: i.Name})
		if err != nil {
			continue
		}
		exits = append(exits, exitCandidate{mark: " ", label: "if=" + i.Name, b: b})
	}

	if len(targets) == 0 {
		line("没有需要探测的目标地址（请配置 stb.auth_host）")
		return false
	}

	sums := make([]struct {
		ok    int
		total int
	}, len(exits))

	for _, target := range targets {
		line("目标 %s", target)
		line("  %-2s %-24s %-16s %-6s %-9s %s", "标记", "出口", "源地址", "结果", "耗时", "说明")
		line("  " + strings.Repeat("-", 84))
		for i, e := range exits {
			r := e.b.Probe(ctx, target, timeout)
			sums[i].total++
			result := "FAIL"
			detail := ""
			if r.OK {
				result = "OK"
				sums[i].ok++
			} else if r.Err != nil {
				detail = r.Err.Error()
			}
			line("  %-4s %-24s %-16s %-8s %-9s %s",
				e.mark, truncateASCII(e.label, 24), r.Source, result,
				r.Elapsed.Round(time.Millisecond).String(), detail)
		}
		line("")
	}

	// 结论
	line("=========================== 结论 ===========================")
	line("（标记列 * 表示 config 里 system.dev 当前指定的出口；")
	line("  default-route 表示不绑定、交给系统路由表）")
	line("")

	var configured *exitCandidate
	okExits := make([]exitCandidate, 0, len(exits))
	for i, e := range exits {
		if sums[i].total > 0 && sums[i].ok == sums[i].total {
			okExits = append(okExits, e)
		}
		if e.mark == "*" {
			ec := e
			configured = &ec
		}
	}

	anyOK := len(okExits) > 0
	switch {
	case configured != nil && okExitsContain(okExits, configured.label):
		line("✅ system.dev=%s 可以连通目标。认证超时与该绑定无关，请继续排查其它环节。", cfgDev)
	case configured != nil && anyOK:
		line("⚠ system.dev=%s 连不通目标，但下面这些出口可以：", cfgDev)
		for _, e := range okExits {
			line("     - %s", e.label)
		}
		line("   建议把 system.dev 改成其中之一（改完需要重启程序）。")
	case configured != nil:
		line("❌ 所有出口（含 system.dev=%s）都连不通目标地址。", cfgDev)
		line("   说明本机网络本身就到达不了该地址，与网卡绑定无关。请检查：")
		line("     1. 本机是否真的接在 IPTV 专网上（专网口是否 UP、是否拿到专网 IP）")
		line("     2. 是否有到该目标的路由 / 策略路由（ip route show table all、ip rule show）")
		line("     3. 目标端口是否可达（telnet / nc 测一下）")
	case anyOK:
		line("ℹ 当前未启用网卡绑定（system.dev 为空），但下列出口可以连通目标：")
		for _, e := range okExits {
			line("     - %s", e.label)
		}
	default:
		line("❌ 所有出口都连不通目标地址，说明本机网络到达不了该地址，")
		line("   与网卡绑定无关，请先解决本机到专网/目标地址的连通性。")
	}
	line("")
	line("手工复核命令（在设备上执行）：")
	for _, t := range targets {
		line("  curl -m 5 -v http://%s/iptv3a/4kLogAuth.do          # 走系统默认路由", t)
		line("  curl -m 5 -v --interface <网卡名> http://%s/iptv3a/4kLogAuth.do", t)
	}
	line("  ip -br addr ; ip route show table all ; ip rule show")

	return anyOK
}

func okExitsContain(list []exitCandidate, label string) bool {
	for _, e := range list {
		if e.label == label {
			return true
		}
	}
	return false
}

// truncateASCII 按字节截断，仅供纯 ASCII 标签使用。
func truncateASCII(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}
