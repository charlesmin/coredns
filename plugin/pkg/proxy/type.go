package proxy

import (
	"context"
	"crypto/tls"
	"net"
	"time"

	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
)

type transportType int

const (
	typeUDP transportType = iota
	typeTCP
	typeTLS
	typeTotalCount // keep this last
)

type IProxy interface {
	Name() string
	Addr() string
	Start(duration time.Duration)
	Stop()
	Connect(context.Context, request.Request, Options) (*dns.Msg, error)
	SetReadTimeout(duration time.Duration)
	SetTLSConfig(cfg *tls.Config)
	SetExpire(expire time.Duration)
	GetHealthchecker() HealthChecker
	Fails() uint32
	IncrementFails()
	ResetFails()
	Healthcheck()
	Down(maxfails uint32) bool
}

func stringToTransportType(s string) transportType {
	switch s {
	case "udp":
		return typeUDP
	case "tcp":
		return typeTCP
	case "tcp-tls":
		return typeTLS
	}

	return typeUDP
}

func (t *Transport) transportTypeFromConn(pc *persistConn) transportType {
	if _, ok := pc.c.Conn.(*net.UDPConn); ok {
		return typeUDP
	}

	if t.tlsConfig == nil {
		return typeTCP
	}

	return typeTLS
}
