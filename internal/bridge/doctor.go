package bridge

import (
	"fmt"
	"net"
	"time"
)

type HealthStatus struct {
	Name   string `json:"name"`
	Addr   string `json:"addr"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

func CheckTCP(host string, port int, timeout time.Duration, name string) HealthStatus {
	addr := fmt.Sprintf("%s:%d", host, port)
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return HealthStatus{Name: name, Addr: addr, OK: false, Detail: err.Error()}
	}
	_ = conn.Close()
	return HealthStatus{Name: name, Addr: addr, OK: true}
}
