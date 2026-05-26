// Package netdial provides protected TCP and HTTP clients (VpnService.protect on Android).
package netdial

import (
	"net"
	"sync/atomic"
	"syscall"
	"time"
)

var socketProtectFuncVal atomic.Value // stores func(fd int)

// SetSocketProtectFunc registers the per-socket protect callback.
// Pass nil to clear it. Thread-safe.
func SetSocketProtectFunc(fn func(fd int)) {
	if fn == nil {
		socketProtectFuncVal.Store((func(int))(nil))
	} else {
		socketProtectFuncVal.Store(fn)
	}
}

// ProtectedDialer returns a net.Dialer whose Control hook calls the registered
// socket protect function (VpnService.protect on Android).
func ProtectedDialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, c syscall.RawConn) error {
			if fn, _ := socketProtectFuncVal.Load().(func(int)); fn != nil {
				return c.Control(func(fd uintptr) { fn(int(fd)) })
			}
			return nil
		},
	}
}
