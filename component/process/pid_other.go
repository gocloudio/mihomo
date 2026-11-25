//go:build !darwin || ios

package process

import (
	"net/netip"

	C "github.com/metacubex/mihomo/constant"
)

// PIDStartTime 在非 macOS 构建中报告不支持，让调用方在登记绑定时就能返回错误。
func PIDStartTime(int32) (uint64, error) { return 0, ErrPlatformNotSupport }

// PIDOwnsSocket 在非 macOS 构建中始终返回未匹配。
func PIDOwnsSocket(int32, C.NetWork, netip.AddrPort, netip.AddrPort) bool { return false }
