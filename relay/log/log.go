// Package log provides optional callbacks for relay, route, and proxy diagnostics.
package log

import (
	"fmt"
	"sync/atomic"
)

// OnRequest is an optional callback triggered for every proxied HTTP request.
var OnRequest func(method, url string)

var logFuncPtr atomic.Pointer[func(level, msg string)]

// SetLogFunc sets the log callback in a race-safe way.
func SetLogFunc(f func(level, msg string)) {
	if f == nil {
		logFuncPtr.Store(nil)
	} else {
		logFuncPtr.Store(&f)
	}
}

// Log writes a structured log line when SetLogFunc is configured.
func Log(level, format string, args ...any) {
	Logf(level, format, args...)
}

// Logf is the internal logging helper used by relay subpackages.
func Logf(level, format string, args ...any) {
	if p := logFuncPtr.Load(); p != nil {
		(*p)(level, fmt.Sprintf(format, args...))
	}
}

// FmtBytes returns a human-readable size string.
func FmtBytes(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
