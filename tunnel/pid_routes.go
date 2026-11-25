package tunnel

import (
	"fmt"
	"net/netip"

	"github.com/metacubex/mihomo/component/process"
	C "github.com/metacubex/mihomo/constant"
)

type pidRoute struct {
	startTime uint64
	proxy     string
}

// pidRoutes 只保存在内存中，与代理节点和订阅表共用 configMux。
var pidRoutes = make(map[int32]pidRoute)

// SetPIDRoute 将当前进程绑定到具体节点，重复设置同一 PID 时覆盖旧绑定。
func SetPIDRoute(pid int32, name string) error {
	startTime, err := process.PIDStartTime(pid)
	if err != nil {
		return fmt.Errorf("cannot read process %d: %w", pid, err)
	}
	configMux.Lock()
	defer configMux.Unlock()
	if pidRouteProxy(name) == nil {
		return fmt.Errorf("expected a unique proxy node, got %q", name)
	}
	pidRoutes[pid] = pidRoute{startTime: startTime, proxy: name}
	return nil
}

// DeletePIDRoute 删除绑定，已建立的连接继续使用原来的出站。
func DeletePIDRoute(pid int32) {
	configMux.Lock()
	defer configMux.Unlock()
	delete(pidRoutes, pid)
}

// pidRouteProxy 先查全局代理表，名称未注册时才在订阅中查找唯一的同名节点。调用时须持有 configMux。
func pidRouteProxy(name string) C.Proxy {
	if proxy := proxies[name]; proxy != nil {
		// 适配器类型枚举中，内置动作和策略组排在具体代理协议之前。
		if proxy.Type() >= C.Shadowsocks {
			return proxy
		}
		return nil
	}
	var match C.Proxy
	for _, provider := range providers {
		for _, proxy := range provider.Proxies() {
			if proxy.Name() == name && proxy.Type() >= C.Shadowsocks {
				if match != nil {
					return nil
				}
				match = proxy
			}
		}
	}
	return match
}

// matchPIDRoute 返回 nil, nil 时继续匹配普通规则。连接归属冲突或节点不可用时返回错误，避免改走其他出站。
func matchPIDRoute(metadata *C.Metadata) (C.Proxy, error) {
	if len(pidRoutes) == 0 || metadata.Type != C.TUN {
		return nil, nil
	}
	source := netip.AddrPortFrom(metadata.SrcIP, metadata.SrcPort)
	destination := metadata.AddrPort()
	// DNS 映射和嗅探可能会改写拨号目标，比较套接字时优先使用 TUN 收到的原始目的地址。
	if raw, ok := metadata.RawDstAddr.(interface{ AddrPort() netip.AddrPort }); ok {
		destination = raw.AddrPort()
	}
	if !source.IsValid() || !destination.IsValid() {
		return nil, nil
	}

	var matchedPID int32
	for pid, route := range pidRoutes {
		if !process.PIDOwnsSocket(pid, metadata.NetWork, source, destination) {
			continue
		}
		// 确认连接归属后再核对启动时间，避免新进程复用旧 PID 时继承绑定。
		if start, err := process.PIDStartTime(pid); err != nil || start != route.startTime {
			continue
		}
		if matchedPID != 0 {
			return nil, fmt.Errorf("ambiguous PID route for %s", source)
		}
		matchedPID = pid
	}
	if matchedPID == 0 {
		return nil, nil
	}
	name := pidRoutes[matchedPID].proxy
	// 按名称从当前配置重新取节点，使配置重载和订阅刷新能更新实际出站。
	proxy := pidRouteProxy(name)
	if proxy == nil {
		return nil, fmt.Errorf("proxy node %q for PID %d is unavailable", name, matchedPID)
	}
	if metadata.NetWork == C.UDP && !proxy.SupportUDP() {
		return nil, fmt.Errorf("proxy node %q for PID %d does not support UDP", name, matchedPID)
	}
	metadata.ProcessID = matchedPID
	return proxy, nil
}
