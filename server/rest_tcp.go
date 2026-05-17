package server

import (
	"net"
	"time"
)

// tcpDialer — server reach 테스트용 짧은 timeout dialer. test/status/reconnect 가 공유.
func tcpDialer() *net.Dialer {
	return &net.Dialer{Timeout: 2 * time.Second}
}
