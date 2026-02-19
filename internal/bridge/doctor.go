package bridge

import (
	"net"
	"strconv"
	"time"
)

type HealthStatus struct {
	Name   string `json:"name"`
	Addr   string `json:"addr"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

func CheckTCP(host string, port int, timeout time.Duration, name string) HealthStatus {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return HealthStatus{Name: name, Addr: addr, OK: false, Detail: err.Error()}
	}
	_ = conn.Close()
	return HealthStatus{Name: name, Addr: addr, OK: true}
}
