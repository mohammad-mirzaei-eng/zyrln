// Package conn provides small connection helpers shared by the MITM proxy and tunnel.
package conn

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// BufferedConn wraps a connection with a buffered reader (e.g. after SOCKS handshake).
type BufferedConn struct {
	net.Conn
	Reader *bufio.Reader
}

func (c *BufferedConn) Read(p []byte) (int, error) {
	return c.Reader.Read(p)
}

// NewBufferedConn wraps conn after SOCKS or CONNECT hijack when bytes may be buffered.
func NewBufferedConn(conn net.Conn, r *bufio.Reader) *BufferedConn {
	return &BufferedConn{Conn: conn, Reader: r}
}

// WriteHTTPError writes a minimal HTTP/1.1 error response on a raw connection.
func WriteHTTPError(conn net.Conn, status int, msg string) {
	resp := &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        make(http.Header),
		Body:          io.NopCloser(strings.NewReader(msg)),
		ContentLength: int64(len(msg)),
	}
	resp.Header.Set("Content-Type", "text/plain")
	resp.Header.Set("Connection", "close")
	_ = resp.Write(conn)
}

// IsLikelyTLS returns true if the buffered stream starts with a TLS handshake record.
func IsLikelyTLS(reader *bufio.Reader) bool {
	peek, err := reader.Peek(1)
	if err != nil {
		return false
	}
	return peek[0] == 0x16
}

// ReadSOCKSAddress decodes a SOCKS5 address from the reader.
func ReadSOCKSAddress(reader io.Reader, atyp byte) (string, error) {
	switch atyp {
	case 0x01:
		addr := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(reader, addr); err != nil {
			return "", err
		}
		return net.IP(addr).String(), nil
	case 0x03:
		var size [1]byte
		if _, err := io.ReadFull(reader, size[:]); err != nil {
			return "", err
		}
		addr := make([]byte, int(size[0]))
		if _, err := io.ReadFull(reader, addr); err != nil {
			return "", err
		}
		return string(addr), nil
	case 0x04:
		addr := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(reader, addr); err != nil {
			return "", err
		}
		return net.IP(addr).String(), nil
	default:
		return "", fmt.Errorf("unsupported socks address type %d", atyp)
	}
}
