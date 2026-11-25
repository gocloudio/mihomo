//go:build !darwin || ios

package process

import (
	"net/netip"

	C "github.com/metacubex/mihomo/constant"
)

func PIDStartTime(int32) (uint64, error) { return 0, ErrPlatformNotSupport }

func PIDOwnsSocket(int32, C.NetWork, netip.AddrPort, netip.AddrPort) bool { return false }
