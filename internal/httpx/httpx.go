// Package httpx holds small HTTP helpers shared by the servers.
package httpx

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
)

// ClientIP returns the best guess at the originating client address. Requests
// normally arrive through the reverse proxy, so X-Forwarded-For wins, then
// X-Real-IP, then the socket address.
func ClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		if first := strings.TrimSpace(strings.Split(forwarded, ",")[0]); first != "" {
			return first
		}
	}
	if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
		return real
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// RequestID returns the caller supplied request id or a fresh random one.
func RequestID(r *http.Request) string {
	if r != nil {
		for _, header := range []string{"X-Request-Id", "X-Correlation-Id"} {
			if id := strings.TrimSpace(r.Header.Get(header)); id != "" {
				return id
			}
		}
	}
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}

// Host returns the host portion of a socket address.
func Host(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}
