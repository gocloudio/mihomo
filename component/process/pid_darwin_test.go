//go:build darwin && !ios

package process

import (
	"net"
	"net/netip"
	"os"
	"testing"

	C "github.com/metacubex/mihomo/constant"
	"github.com/stretchr/testify/require"
)

func TestPIDOwnsConnectedUDPSocket(t *testing.T) {
	// 用真实 IPv4 和 IPv6 套接字核对内核端口、地址和协议的解析。
	for _, ip := range []string{"127.0.0.1", "::1"} {
		t.Run(ip, func(t *testing.T) {
			addr := &net.UDPAddr{IP: net.ParseIP(ip)}
			server, err := net.ListenUDP("udp", addr)
			require.NoError(t, err)
			defer server.Close()
			remote := server.LocalAddr().(*net.UDPAddr)
			client, err := net.DialUDP("udp", addr, remote)
			require.NoError(t, err)
			defer client.Close()

			pid := int32(os.Getpid())
			source := client.LocalAddr().(*net.UDPAddr).AddrPort()
			destination := remote.AddrPort()
			require.True(t, PIDOwnsSocket(pid, C.UDP, source, destination))
			require.False(t, PIDOwnsSocket(pid, C.UDP, source, netip.AddrPortFrom(destination.Addr(), 1)))
			require.False(t, PIDOwnsSocket(pid, C.TCP, source, destination))
		})
	}
}
