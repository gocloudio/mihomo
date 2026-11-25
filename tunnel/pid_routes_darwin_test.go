//go:build darwin && !ios

package tunnel

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	N "github.com/metacubex/mihomo/common/net"
	C "github.com/metacubex/mihomo/constant"
	P "github.com/metacubex/mihomo/constant/provider"
	"github.com/stretchr/testify/require"
)

type pidTestProxy struct {
	C.Proxy
	name string
	conn *net.UDPConn
}

func (p *pidTestProxy) Name() string        { return p.name }
func (p *pidTestProxy) Type() C.AdapterType { return C.Socks5 }
func (p *pidTestProxy) SupportUDP() bool    { return true }

func (p *pidTestProxy) ListenPacketContext(context.Context, *C.Metadata) (C.PacketConn, error) {
	return &pidTestPacketConn{EnhancePacketConn: N.NewEnhancePacketConn(p.conn), name: p.name}, nil
}

type pidTestPacketConn struct {
	N.EnhancePacketConn
	name string
}

func (p *pidTestPacketConn) Chains() C.Chain                             { return C.Chain{p.name} }
func (*pidTestPacketConn) ProviderChains() C.Chain                       { return nil }
func (*pidTestPacketConn) AppendToChains(C.ProxyAdapter)                 {}
func (*pidTestPacketConn) RemoteDestination() string                     { return "" }
func (*pidTestPacketConn) ResolveUDP(context.Context, *C.Metadata) error { return nil }

type pidTestProvider struct {
	P.ProxyProvider
	node C.Proxy
}

func (p pidTestProvider) Proxies() []C.Proxy { return []C.Proxy{p.node} }

func startPIDTestProcess(t *testing.T, cmd *exec.Cmd) func() {
	t.Helper()
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	var once sync.Once
	stop := func() { once.Do(func() { _ = stdin.Close(); _ = cmd.Wait() }) }
	t.Cleanup(stop)
	return stop
}

func TestPIDRouteKeepsProviderAfterReload(t *testing.T) {
	original := &pidTestProxy{name: "shared"}
	refreshed := &pidTestProxy{name: "shared"}
	global := &pidTestProxy{name: "shared"}
	t.Cleanup(func() { UpdateProxies(nil, nil) })
	UpdateProxies(nil, map[string]P.ProxyProvider{"subscription": pidTestProvider{node: original}})
	pid := int32(os.Getpid())
	require.NoError(t, SetPIDRoute(pid, original.name))
	t.Cleanup(func() { DeletePIDRoute(pid) })

	// 重载后仍从原订阅取得更新后的节点，即使全局配置新增了同名节点。
	UpdateProxies(map[string]C.Proxy{global.name: global}, map[string]P.ProxyProvider{"subscription": pidTestProvider{node: refreshed}})
	configMux.RLock()
	matched := pidRouteMatch{pid: pid, route: pidRoutes[pid]}
	configMux.RUnlock()
	node, err := matched.outbound(C.TCP)
	require.NoError(t, err)
	require.Same(t, refreshed, node)
	UpdateProxies(map[string]C.Proxy{global.name: global}, nil)
	_, err = matched.outbound(C.TCP)
	require.ErrorContains(t, err, "is unavailable")
}

func TestPIDRoutePrunesExitedAndReusedProcesses(t *testing.T) {
	UpdateProxies(map[string]C.Proxy{"node": &pidTestProxy{name: "node"}}, nil)
	t.Cleanup(func() { UpdateProxies(nil, nil) })
	exited, reused := exec.Command("/bin/cat"), exec.Command("/bin/cat")
	stopExited := startPIDTestProcess(t, exited)
	startPIDTestProcess(t, reused)
	livePID, exitedPID, reusedPID := int32(os.Getpid()), int32(exited.Process.Pid), int32(reused.Process.Pid)
	for _, pid := range []int32{livePID, exitedPID, reusedPID} {
		require.NoError(t, SetPIDRoute(pid, "node"))
		boundPID := pid
		t.Cleanup(func() { DeletePIDRoute(boundPID) })
	}
	stopExited()
	configMux.Lock()
	// 保留真实运行的进程，只更改历史记录中的启动时间来模拟 PID 复用。
	route := pidRoutes[reusedPID]
	route.startTime--
	pidRoutes[reusedPID] = route
	pidRouteCleanup.Reset(time.Millisecond)
	configMux.Unlock()

	require.Eventually(t, func() bool {
		configMux.RLock()
		defer configMux.RUnlock()
		_, kept := pidRoutes[livePID]
		_, exited := pidRoutes[exitedPID]
		_, recycled := pidRoutes[reusedPID]
		return kept && !exited && !recycled
	}, time.Second, time.Millisecond)
}

type pidTestPacket struct {
	source *net.UDPAddr
}

func (*pidTestPacket) Data() []byte                                { return []byte("payload") }
func (*pidTestPacket) Drop()                                       {}
func (*pidTestPacket) WriteBack(b []byte, _ net.Addr) (int, error) { return len(b), nil }
func (p *pidTestPacket) LocalAddr() net.Addr                       { return p.source }

func TestPIDRouteUDPPortReuse(t *testing.T) {
	listen := func() *net.UDPConn {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	nodeA := &pidTestProxy{name: "node-a", conn: listen()}
	nodeB := &pidTestProxy{name: "node-b", conn: listen()}
	UpdateProxies(map[string]C.Proxy{nodeA.name: nodeA, nodeB.name: nodeB}, nil)
	t.Cleanup(func() { UpdateProxies(nil, nil) })
	OnRunning()
	t.Cleanup(OnSuspend)

	start := func(source, target *net.UDPAddr) (int32, *net.UDPAddr, func()) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestPIDRouteUDPClient$", "--", source.String(), target.String())
		stdout, err := cmd.StdoutPipe()
		require.NoError(t, err)
		stop := startPIDTestProcess(t, cmd)
		var address string
		_, err = fmt.Fscanln(stdout, &address)
		require.NoError(t, err)
		return int32(cmd.Process.Pid), net.UDPAddrFromAddrPort(netip.MustParseAddrPort(address)), stop
	}

	send := func(source *net.UDPAddr, target *net.UDPConn, node *pidTestProxy) {
		destination := target.LocalAddr().(*net.UDPAddr)
		metadata := &C.Metadata{
			Type: C.TUN, NetWork: C.UDP, SrcIP: source.AddrPort().Addr(), SrcPort: uint16(source.Port),
			DstIP: destination.AddrPort().Addr(), DstPort: uint16(destination.Port), RawDstAddr: destination,
		}
		handleUDPConn(C.NewPacketAdapter(&pidTestPacket{source}, metadata))
		require.NoError(t, target.SetReadDeadline(time.Now().Add(3*time.Second)))
		payload := make([]byte, 32)
		n, from, err := target.ReadFromUDP(payload)
		require.NoError(t, err)
		require.Equal(t, "payload", string(payload[:n]))
		require.Equal(t, node.conn.LocalAddr().String(), from.String())
	}

	// 两个进程使用同一对源地址和目标地址，确保旧会话仍在时按进程隔离。
	target := listen()
	destination := target.LocalAddr().(*net.UDPAddr)
	pidA, source, stopA := start(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, destination)
	require.NoError(t, SetPIDRoute(pidA, nodeA.name))
	t.Cleanup(func() { DeletePIDRoute(pidA) })
	send(source, target, nodeA)
	stopA()

	pidB, reusedSource, _ := start(source, destination)
	require.NotEqual(t, pidA, pidB)
	require.Equal(t, source, reusedSource)
	require.NoError(t, SetPIDRoute(pidB, nodeB.name))
	t.Cleanup(func() { DeletePIDRoute(pidB) })
	send(reusedSource, target, nodeB)
}

func TestPIDRouteUDPClient(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-3] != "--" {
		return
	}
	local := net.UDPAddrFromAddrPort(netip.MustParseAddrPort(os.Args[len(os.Args)-2]))
	remote := net.UDPAddrFromAddrPort(netip.MustParseAddrPort(os.Args[len(os.Args)-1]))
	conn, err := net.DialUDP("udp4", local, remote)
	require.NoError(t, err)
	defer conn.Close()
	fmt.Println(conn.LocalAddr())
	var signal [1]byte
	_, _ = os.Stdin.Read(signal[:])
}
