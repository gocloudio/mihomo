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

// pidRoutes is guarded by configMux along with the outbounds it references.
var pidRoutes = make(map[int32]pidRoute)

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

func DeletePIDRoute(pid int32) {
	configMux.Lock()
	defer configMux.Unlock()
	delete(pidRoutes, pid)
}

func pidRouteProxy(name string) C.Proxy {
	if proxy := proxies[name]; proxy != nil {
		// Built-in actions and proxy groups precede the concrete protocol types.
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

func matchPIDRoute(metadata *C.Metadata) (C.Proxy, error) {
	if len(pidRoutes) == 0 || metadata.Type != C.TUN {
		return nil, nil
	}
	source := netip.AddrPortFrom(metadata.SrcIP, metadata.SrcPort)
	destination := metadata.AddrPort()
	// DNS mapping and sniffing may have changed the destination used for dialing.
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
