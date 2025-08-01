package proxy

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/coredns/coredns/plugin/pkg/transport"
	"github.com/miekg/dns"
)

// a persistConn hold the dns.Conn and the last used time.
type persistConn struct {
	c     *dns.Conn
	trans string
	host  string
	used  time.Time
}

// Transport hold the persistent cache.
type Transport struct {
	avgDialTime int64                          // kind of average time of dial time
	conns       [typeTotalCount][]*persistConn // Buckets for udp, tcp and tcp-tls.
	expire      time.Duration                  // After this duration a connection is expired.
	trans       string
	addr        string
	tlsConfig   *tls.Config
	proxyName   string

	dial  chan string
	yield chan *persistConn
	ret   chan *persistConn
	stop  chan bool
}

func (pc *persistConn) writeHttpRequest(m *dns.Msg) (err error) {
	var packedMsg []byte
	packedMsg, err = m.Pack()
	if err != nil {
		return
	}

	var buf bytes.Buffer
	var req *http.Request
	req, err = http.NewRequest(http.MethodGet, fmt.Sprintf("%s?dns=%s", "/dns-query", base64.RawURLEncoding.EncodeToString(packedMsg)), nil)
	if err != nil {
		return
	}
	req.Host = pc.host
	req.Header.Add("Accept", "application/dns-message")
	req.Write(&buf)

	_, err = pc.c.Conn.Write(buf.Bytes())
	return
}

func (pc *persistConn) readHttpResponse() (b []byte, err error) {
	res, err := http.ReadResponse(bufio.NewReader(pc.c.Conn), nil)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	_, err = io.Copy(bufio.NewWriter(&buf), res.Body)
	if err != nil {
		return
	}

	b = buf.Bytes()
	return
}

func (pc *persistConn) writeMsg(m *dns.Msg) (err error) {
	if pc.trans == transport.HTTPS {
		err = pc.writeHttpRequest(m)
		return
	}
	return pc.c.WriteMsg(m)
}

func (pc *persistConn) readMsg() (m *dns.Msg, err error) {
	if pc.trans == transport.HTTPS {
		var b []byte
		b, err = pc.readHttpResponse()
		if err != nil {
			return
		}

		m = new(dns.Msg)
		err = m.Unpack(b)
		return
	}

	m, err = pc.c.ReadMsg()
	return
}

func newTransport(proxyName, addr, trans string) *Transport {
	t := &Transport{
		avgDialTime: int64(maxDialTimeout / 2),
		conns:       [typeTotalCount][]*persistConn{},
		trans:       trans,
		expire:      defaultExpire,
		addr:        addr,
		dial:        make(chan string),
		yield:       make(chan *persistConn),
		ret:         make(chan *persistConn),
		stop:        make(chan bool),
		proxyName:   proxyName,
	}
	return t
}

// connManager manages the persistent connection cache for UDP and TCP.
func (t *Transport) connManager() {
	ticker := time.NewTicker(defaultExpire)
	defer ticker.Stop()
Wait:
	for {
		select {
		case proto := <-t.dial:
			transtype := stringToTransportType(proto)
			// take the last used conn - complexity O(1)
			if stack := t.conns[transtype]; len(stack) > 0 {
				pc := stack[len(stack)-1]
				if time.Since(pc.used) < t.expire {
					// Found one, remove from pool and return this conn.
					t.conns[transtype] = stack[:len(stack)-1]
					t.ret <- pc
					continue Wait
				}
				// clear entire cache if the last conn is expired
				t.conns[transtype] = nil
				// now, the connections being passed to closeConns() are not reachable from
				// transport methods anymore. So, it's safe to close them in a separate goroutine
				go closeConns(stack)
			}
			t.ret <- nil

		case pc := <-t.yield:
			transtype := t.transportTypeFromConn(pc)
			t.conns[transtype] = append(t.conns[transtype], pc)

		case <-ticker.C:
			t.cleanup(false)

		case <-t.stop:
			t.cleanup(true)
			close(t.ret)
			return
		}
	}
}

// closeConns closes connections.
func closeConns(conns []*persistConn) {
	for _, pc := range conns {
		pc.c.Close()
	}
}

// cleanup removes connections from cache.
func (t *Transport) cleanup(all bool) {
	staleTime := time.Now().Add(-t.expire)
	for transtype, stack := range t.conns {
		if len(stack) == 0 {
			continue
		}
		if all {
			t.conns[transtype] = nil
			// now, the connections being passed to closeConns() are not reachable from
			// transport methods anymore. So, it's safe to close them in a separate goroutine
			go closeConns(stack)
			continue
		}
		if stack[0].used.After(staleTime) {
			continue
		}

		// connections in stack are sorted by "used"
		good := sort.Search(len(stack), func(i int) bool {
			return stack[i].used.After(staleTime)
		})
		t.conns[transtype] = stack[good:]
		// now, the connections being passed to closeConns() are not reachable from
		// transport methods anymore. So, it's safe to close them in a separate goroutine
		go closeConns(stack[:good])
	}
}

// It is hard to pin a value to this, the import thing is to no block forever, losing at cached connection is not terrible.
const yieldTimeout = 25 * time.Millisecond

// Yield returns the connection to transport for reuse.
func (t *Transport) Yield(pc *persistConn) {
	pc.used = time.Now() // update used time

	// Make this non-blocking, because in the case of a very busy forwarder we will *block* on this yield. This
	// blocks the outer go-routine and stuff will just pile up.  We timeout when the send fails to as returning
	// these connection is an optimization anyway.
	select {
	case t.yield <- pc:
		return
	case <-time.After(yieldTimeout):
		return
	}
}

// Start starts the transport's connection manager.
func (t *Transport) Start() { go t.connManager() }

// Stop stops the transport's connection manager.
func (t *Transport) Stop() { close(t.stop) }

// SetExpire sets the connection expire time in transport.
func (t *Transport) SetExpire(expire time.Duration) { t.expire = expire }

// SetTLSConfig sets the TLS config in transport.
func (t *Transport) SetTLSConfig(cfg *tls.Config) { t.tlsConfig = cfg }

const (
	defaultExpire  = 10 * time.Second
	minDialTimeout = 1 * time.Second
	maxDialTimeout = 30 * time.Second
)
