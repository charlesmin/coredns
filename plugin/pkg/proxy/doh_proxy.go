package proxy

import (
	"crypto/tls"
	"net"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/coredns/coredns/plugin/pkg/log"
	"github.com/coredns/coredns/plugin/pkg/up"
	"golang.org/x/net/http2"
)

const (
	defaultDialTimeout           = 3 * time.Second
	defaultTLSHandshakeTimeout   = 5 * time.Second
	defaultResponseHeaderTimeout = 5 * time.Second
	defaultIdleConnTimeout       = 60 * time.Second
	defaultHttpRequestTimeout    = 10 * time.Second
)

// DOHProxy defines an upstream host.
type DOHProxy struct {
	fails     uint32
	addr      string
	proxyName string
	baseURL   string

	transport *http.Transport
	client    *http.Client

	readTimeout time.Duration

	// health checking
	probe  *up.Probe
	health HealthChecker
}

// NewDOHProxy returns a new doh DOHProxy.
func NewDOHProxy(proxyName, addr, trans string) *DOHProxy {
	tr := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: defaultDialTimeout,
		}).DialContext,
		TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
		ResponseHeaderTimeout: defaultResponseHeaderTimeout,
		IdleConnTimeout:       defaultIdleConnTimeout,
	}

	p := &DOHProxy{
		addr:        addr,
		fails:       0,
		probe:       up.New(),
		readTimeout: 2 * time.Second,
		health:      NewHealthChecker(proxyName, trans, true, "."),
		proxyName:   proxyName,
		baseURL:     "https://" + addr,
		transport:   tr,
		client: &http.Client{
			Transport: tr,
			Timeout:   defaultHttpRequestTimeout,
		},
	}

	runtime.SetFinalizer(p, (*DOHProxy).finalizer)
	return p
}

func (p *DOHProxy) Name() string { return p.proxyName }
func (p *DOHProxy) Addr() string { return p.addr }

// SetTLSConfig sets the TLS config in the lower p.transport and in the healthchecking client.
func (p *DOHProxy) SetTLSConfig(cfg *tls.Config) {
	p.health.SetTLSConfig(cfg)
	p.transport.TLSClientConfig = cfg
	http2.ConfigureTransport(p.transport)
}

// SetExpire sets the expire duration in the lower p.transport.
func (p *DOHProxy) SetExpire(expire time.Duration) {
	p.transport.IdleConnTimeout = expire
}

func (p *DOHProxy) GetHealthchecker() HealthChecker {
	return p.health
}

func (p *DOHProxy) Fails() uint32 {
	return atomic.LoadUint32(&p.fails)
}

// Healthcheck kicks of a round of health checks for this DOHProxy.
func (p *DOHProxy) Healthcheck() {
	if p.health == nil {
		log.Warning("No healthchecker")
		return
	}

	p.probe.Do(func() error {
		return p.health.Check(p)
	})
}

// Down returns true if this DOHProxy is down, i.e. has *more* fails than maxfails.
func (p *DOHProxy) Down(maxfails uint32) bool {
	if maxfails == 0 {
		return false
	}

	fails := atomic.LoadUint32(&p.fails)
	return fails > maxfails
}

// Stop close stops the health checking goroutine.
func (p *DOHProxy) Stop()      { p.probe.Stop() }
func (p *DOHProxy) finalizer() {}

// Start starts the DOHProxy's healthchecking.
func (p *DOHProxy) Start(duration time.Duration) {
	p.probe.Start(duration)
}

func (p *DOHProxy) SetReadTimeout(duration time.Duration) {
	p.readTimeout = duration
}

// IncrementFails increments the number of fails safely.
func (p *DOHProxy) IncrementFails() {
	curVal := atomic.LoadUint32(&p.fails)
	if curVal > curVal+1 {
		// overflow occurred, do not update the counter again
		return
	}
	atomic.AddUint32(&p.fails, 1)
}

// ResetFails resets the number of fails safely.
func (p *DOHProxy) ResetFails() {
	atomic.StoreUint32(&p.fails, 0)
}

var (
	_ IProxy = (*DOHProxy)(nil)
)
