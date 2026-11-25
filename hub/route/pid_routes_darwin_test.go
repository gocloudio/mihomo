//go:build darwin && !ios

package route

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/metacubex/http"
	"github.com/metacubex/http/httptest"
	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/adapter/outbound"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/listener/sing"
	R "github.com/metacubex/mihomo/rules/common"
	"github.com/metacubex/mihomo/tunnel"
	M "github.com/metacubex/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

func TestPIDRoutesChooseConcreteNodes(t *testing.T) {
	// 本地代理在 CONNECT 成功后返回自己的节点名，测试据此核对连接实际经过的出站。
	nodes := make(map[string]C.Proxy)
	for _, name := range []string{"node-a", "node-b", "fallback"} {
		nodeName := name
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodConnect {
				t.Error("proxy did not receive CONNECT")
				return
			}
			conn, rw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			_, _ = fmt.Fprintf(rw, "HTTP/1.1 200 Connection Established\r\n\r\n%s\n", nodeName)
			_ = rw.Flush()
			_, _ = io.Copy(io.Discard, rw)
		}))
		t.Cleanup(server.Close)
		address := M.ParseSocksaddr(strings.TrimPrefix(server.URL, "http://"))
		node, err := outbound.NewHttp(outbound.HttpOption{
			Name: name, Server: address.Addr.String(), Port: int(address.Port),
		})
		require.NoError(t, err)
		nodes[name] = adapter.NewProxy(node)
	}
	oldProxies, oldProviders := tunnel.Proxies(), tunnel.Providers()
	t.Cleanup(func() { tunnel.UpdateProxies(oldProxies, oldProviders) })
	tunnel.UpdateProxies(nodes, nil)
	tunnel.UpdateRules([]C.Rule{R.NewMatch("fallback")}, nil, nil)
	t.Cleanup(func() { tunnel.UpdateRules(nil, nil, nil) })
	tunnel.OnRunning()
	t.Cleanup(tunnel.OnSuspend)

	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	acceptTUN, err := sing.NewListenerHandler(sing.ListenerConfig{Tunnel: tunnel.Tunnel, Type: C.TUN})
	require.NoError(t, err)
	acceptHTTP, err := sing.NewListenerHandler(sing.ListenerConfig{Tunnel: tunnel.Tunnel, Type: C.HTTP})
	require.NoError(t, err)

	api := router(false, "test-secret", "", Cors{})
	request := func(method string, pid int, body, token string, status int) {
		t.Helper()
		req := httptest.NewRequest(method, fmt.Sprintf("/pid-routes/%d", pid), strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, req)
		require.Equal(t, status, rec.Code, rec.Body.String())
	}

	// 用同一路径的测试二进制启动两个进程，验证不同 PID 可以命中不同节点。
	start := func() (int, func(*sing.ListenerHandler, string)) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestPIDRouteClient$", "--", "pid-route-client")
		stdin, err := cmd.StdinPipe()
		require.NoError(t, err)
		stdout, err := cmd.StdoutPipe()
		require.NoError(t, err)
		cmd.Stderr = os.Stderr
		require.NoError(t, cmd.Start())
		pid := cmd.Process.Pid
		t.Cleanup(func() {
			tunnel.DeletePIDRoute(int32(pid))
			_ = stdin.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
		output := bufio.NewReader(stdout)
		return pid, func(handler *sing.ListenerHandler, want string) {
			t.Helper()
			_, err := fmt.Fprintln(stdin, listener.Addr())
			require.NoError(t, err)
			require.NoError(t, listener.SetDeadline(time.Now().Add(5*time.Second)))
			conn, err := listener.AcceptTCP()
			require.NoError(t, err)
			defer conn.Close()
			meta := M.Metadata{
				Source:      M.ParseSocksaddr(conn.RemoteAddr().String()),
				Destination: M.ParseSocksaddr(conn.LocalAddr().String()),
			}
			go func() { _ = handler.NewConnection(context.Background(), conn, meta) }()
			line, err := output.ReadString('\n')
			require.NoError(t, err)
			require.Equal(t, want+"\n", line)
		}
	}

	pidA, clientA := start()
	pidB, clientB := start()
	request(http.MethodPut, pidA, `{"proxy":"node-a"}`, "wrong", http.StatusUnauthorized)
	request(http.MethodPut, pidA, `{"proxy":"missing"}`, "test-secret", http.StatusBadRequest)
	request(http.MethodPut, pidA, `{"proxy":"node-a"}`, "test-secret", http.StatusNoContent)
	request(http.MethodPut, pidB, `{"proxy":"node-b"}`, "test-secret", http.StatusNoContent)
	clientA(acceptTUN, "node-a")
	clientB(acceptTUN, "node-b")
	clientA(acceptHTTP, "fallback")
	request(http.MethodPut, pidA, `{"proxy":"node-b"}`, "test-secret", http.StatusNoContent)
	clientA(acceptTUN, "node-b")
	request(http.MethodDelete, pidA, "", "test-secret", http.StatusNoContent)
	clientA(acceptTUN, "fallback")
	clientB(acceptTUN, "node-b")
}

func TestPIDRouteClient(t *testing.T) {
	if os.Args[len(os.Args)-1] != "pid-route-client" {
		return
	}
	input := bufio.NewScanner(os.Stdin)
	for input.Scan() {
		conn, err := net.DialTimeout("tcp4", input.Text(), 5*time.Second)
		if err != nil {
			fmt.Fprintln(os.Stdout, err)
			break
		}
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = conn.Write([]byte("test"))
		line, err := bufio.NewReader(conn).ReadString('\n')
		_ = conn.Close()
		if err != nil {
			fmt.Fprintln(os.Stdout, err)
			break
		}
		fmt.Fprint(os.Stdout, line)
	}
}
