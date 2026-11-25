package tunnel

import (
	"errors"
	"fmt"
	"net/netip"
	"syscall"
	"time"

	"github.com/metacubex/mihomo/component/process"
	C "github.com/metacubex/mihomo/constant"
	P "github.com/metacubex/mihomo/constant/provider"
)

type pidRoute struct {
	startTime uint64
	proxy     string
	provider  *string // nil 表示配置中的全局节点。
}

type pidRouteMatch struct {
	pid         int32
	route       pidRoute
	destination netip.AddrPort
}

const pidRouteCleanupInterval = 30 * time.Second

// PID 绑定和清理定时器与代理节点、订阅表共用 configMux。
var (
	pidRoutes       = make(map[int32]pidRoute)
	pidRouteCleanup *time.Timer
)

// SetPIDRoute 将当前进程绑定到具体节点，重复设置同一 PID 时覆盖旧绑定。
func SetPIDRoute(pid int32, name string) error {
	startTime, err := process.PIDStartTime(pid)
	if err != nil {
		return fmt.Errorf("cannot read process %d: %w", pid, err)
	}
	configMux.Lock()
	defer configMux.Unlock()
	provider, ok := pidRouteProvider(name)
	if !ok {
		return fmt.Errorf("expected a unique proxy node, got %q", name)
	}
	pidRoutes[pid] = pidRoute{startTime: startTime, proxy: name, provider: provider}
	if pidRouteCleanup == nil {
		pidRouteCleanup = time.AfterFunc(pidRouteCleanupInterval, prunePIDRoutes)
	}
	return nil
}

// DeletePIDRoute 删除绑定。已建立的 TCP 连接继续使用原来的出站。
func DeletePIDRoute(pid int32) {
	configMux.Lock()
	defer configMux.Unlock()
	delete(pidRoutes, pid)
}

func prunePIDRoutes() {
	configMux.Lock()
	defer configMux.Unlock()
	for pid, route := range pidRoutes {
		start, err := process.PIDStartTime(pid)
		// 只清理已确认退出或被复用的 PID，保留暂时无法查询的绑定。
		if errors.Is(err, syscall.ESRCH) || (err == nil && start != route.startTime) {
			delete(pidRoutes, pid)
		}
	}
	if len(pidRoutes) == 0 {
		pidRouteCleanup = nil
		return
	}
	pidRouteCleanup.Reset(pidRouteCleanupInterval)
}

// pidRouteProvider 在登记时确定节点来源。调用时须持有 configMux。
func pidRouteProvider(name string) (*string, bool) {
	if proxy := proxies[name]; proxy != nil {
		// 适配器类型枚举中，内置动作和策略组排在具体代理协议之前。
		return nil, proxy.Type() >= C.Shadowsocks
	}
	var match *string
	for providerName, provider := range providers {
		proxy, ambiguous := pidProviderProxy(provider, name)
		if ambiguous || (proxy != nil && match != nil) {
			return nil, false
		}
		if proxy != nil {
			source := providerName
			match = &source
		}
	}
	return match, match != nil
}

func pidProviderProxy(provider P.ProxyProvider, name string) (C.Proxy, bool) {
	if provider == nil {
		return nil, false
	}
	var match C.Proxy
	for _, proxy := range provider.Proxies() {
		if proxy.Name() == name && proxy.Type() >= C.Shadowsocks {
			if match != nil {
				return nil, true
			}
			match = proxy
		}
	}
	return match, false
}

func matchPIDRoute(metadata *C.Metadata) (pidRouteMatch, error) {
	if metadata.Type != C.TUN || metadata.SpecialProxy != "" || mode == Direct || mode == Global {
		return pidRouteMatch{}, nil
	}
	configMux.RLock()
	defer configMux.RUnlock()
	if len(pidRoutes) == 0 {
		return pidRouteMatch{}, nil
	}
	source := netip.AddrPortFrom(metadata.SrcIP, metadata.SrcPort)
	destination := metadata.AddrPort()
	// DNS 映射和嗅探可能会改写拨号目标，比较套接字时优先使用 TUN 收到的原始目的地址。
	if raw, ok := metadata.RawDstAddr.(interface{ AddrPort() netip.AddrPort }); ok {
		destination = raw.AddrPort()
	}
	if !source.IsValid() || !destination.IsValid() {
		return pidRouteMatch{}, nil
	}

	var matched pidRouteMatch
	for pid, route := range pidRoutes {
		if !process.PIDOwnsSocket(pid, metadata.NetWork, source, destination) {
			continue
		}
		// 确认连接归属后再核对启动时间，避免新进程复用旧 PID 时继承绑定。
		if start, err := process.PIDStartTime(pid); err != nil || start != route.startTime {
			continue
		}
		if matched.pid != 0 {
			return pidRouteMatch{}, fmt.Errorf("ambiguous PID route for %s", source)
		}
		matched = pidRouteMatch{pid: pid, route: route, destination: destination}
	}
	return matched, nil
}

func (matched pidRouteMatch) outbound(network C.NetWork) (C.Proxy, error) {
	configMux.RLock()
	defer configMux.RUnlock()
	// 只在绑定时选定的来源中查找当前节点，允许订阅更新节点参数。
	name := matched.route.proxy
	var proxy C.Proxy
	if matched.route.provider != nil {
		proxy, _ = pidProviderProxy(providers[*matched.route.provider], name)
	} else if candidate := proxies[name]; candidate != nil && candidate.Type() >= C.Shadowsocks {
		proxy = candidate
	}
	if proxy == nil {
		return nil, fmt.Errorf("proxy node %q for PID %d is unavailable", name, matched.pid)
	}
	if network == C.UDP && !proxy.SupportUDP() {
		return nil, fmt.Errorf("proxy node %q for PID %d does not support UDP", name, matched.pid)
	}
	return proxy, nil
}
