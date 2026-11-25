//go:build darwin && !ios

package process

import (
	"encoding/binary"
	"net/netip"
	"syscall"
	"unsafe"

	C "github.com/metacubex/mihomo/constant"
)

const (
	procPIDListFDs     = 1
	procPIDTBSDInfo    = 3
	procInfoPIDFD      = 3
	procPIDSocketInfo  = 3
	procFDTypeSocket   = 2
	socketKindInternet = 1
	socketKindTCP      = 2

	// Layout of the macOS <sys/proc_info.h> structs on arm64 and amd64.
	procFDInfoSize    = 8
	socketFDInfoSize  = 792
	socketProtocol    = 180
	socketKind        = 256
	socketRemotePort  = 264
	socketLocalPort   = 268
	socketIPFlags     = 288
	socketRemoteIP    = 296
	socketLocalIP     = 312
	procBSDInfoSize   = 136
	procStartSeconds  = 120
	procStartUseconds = 128
)

func PIDStartTime(pid int32) (uint64, error) {
	var info [procBSDInfoSize]byte
	n, _, errno := syscall.Syscall6(syscall.SYS_PROC_INFO, proccallnumpidinfo, uintptr(pid),
		procPIDTBSDInfo, 0, uintptr(unsafe.Pointer(&info[0])), uintptr(len(info)))
	if errno != 0 {
		return 0, errno
	}
	if n != uintptr(len(info)) {
		return 0, ErrNotFound
	}
	return binary.NativeEndian.Uint64(info[procStartSeconds:])*1_000_000 +
		binary.NativeEndian.Uint64(info[procStartUseconds:]), nil
}

func PIDOwnsSocket(pid int32, network C.NetWork, source, destination netip.AddrPort) bool {
	var info [socketFDInfoSize]byte
	for fds := make([]byte, 256*procFDInfoSize); ; fds = make([]byte, len(fds)*2) {
		n, _, errno := syscall.Syscall6(syscall.SYS_PROC_INFO, proccallnumpidinfo, uintptr(pid),
			procPIDListFDs, 0, uintptr(unsafe.Pointer(&fds[0])), uintptr(len(fds)))
		if errno != 0 {
			return false
		}
		if n == uintptr(len(fds)) {
			continue
		}
		for fd := 0; fd+procFDInfoSize <= int(n); fd += procFDInfoSize {
			if binary.NativeEndian.Uint32(fds[fd+4:]) != procFDTypeSocket {
				continue
			}
			fdNumber := binary.NativeEndian.Uint32(fds[fd:])
			size, _, errno := syscall.Syscall6(syscall.SYS_PROC_INFO, procInfoPIDFD, uintptr(pid),
				procPIDSocketInfo, uintptr(fdNumber), uintptr(unsafe.Pointer(&info[0])), uintptr(len(info)))
			if errno == 0 && size == uintptr(len(info)) && matchPIDSocket(info[:], network, source, destination) {
				return true
			}
		}
		return false
	}
}

func matchPIDSocket(info []byte, network C.NetWork, source, destination netip.AddrPort) bool {
	kind := binary.NativeEndian.Uint32(info[socketKind:])
	switch network {
	case C.TCP:
		if kind != socketKindTCP {
			return false
		}
	case C.UDP:
		if kind != socketKindInternet || binary.NativeEndian.Uint32(info[socketProtocol:]) != syscall.IPPROTO_UDP {
			return false
		}
	default:
		return false
	}

	// shortcut: UDP sockets without a remote endpoint fall through until their ownership can be determined.
	return source.Port() == socketPort(info[socketLocalPort:]) &&
		destination.Port() == socketPort(info[socketRemotePort:]) &&
		matchSocketAddress(info[socketLocalIP:], info[socketIPFlags], source.Addr()) &&
		matchSocketAddress(info[socketRemoteIP:], info[socketIPFlags], destination.Addr())
}

func socketPort(info []byte) uint16 {
	port := uint16(binary.NativeEndian.Uint32(info))
	return port>>8 | port<<8
}

func matchSocketAddress(info []byte, flags byte, ip netip.Addr) bool {
	ip = ip.Unmap().WithZone("")
	if ip.Is4() && flags&1 != 0 {
		addr, _ := netip.AddrFromSlice(info[12:16])
		return addr == ip
	}
	if flags&2 != 0 {
		addr, _ := netip.AddrFromSlice(info[:16])
		return addr.Unmap() == ip
	}
	return false
}
