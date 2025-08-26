package forward

import (
	"sync/atomic"
	"time"

	"github.com/coredns/coredns/plugin/pkg/proxy"
	"github.com/coredns/coredns/plugin/pkg/rand"
)

// Policy defines a policy we use for selecting upstreams.
type Policy interface {
	List([]proxy.IProxy) []proxy.IProxy
	String() string
}

// random is a policy that implements random upstream selection.
type random struct{}

func (r *random) String() string { return "random" }

func (r *random) List(p []proxy.IProxy) []proxy.IProxy {
	switch len(p) {
	case 1:
		return p
	case 2:
		if rn.Int()%2 == 0 {
			return []proxy.IProxy{p[1], p[0]} // swap
		}
		return p
	}

	perms := rn.Perm(len(p))
	rnd := make([]proxy.IProxy, len(p))

	for i, p1 := range perms {
		rnd[i] = p[p1]
	}
	return rnd
}

// roundRobin is a policy that selects hosts based on round robin ordering.
type roundRobin struct {
	robin uint32
}

func (r *roundRobin) String() string { return "round_robin" }

func (r *roundRobin) List(p []proxy.IProxy) []proxy.IProxy {
	poolLen := uint32(len(p))
	i := atomic.AddUint32(&r.robin, 1) % poolLen

	robin := []proxy.IProxy{p[i]}
	robin = append(robin, p[:i]...)
	robin = append(robin, p[i+1:]...)

	return robin
}

// sequential is a policy that selects hosts based on sequential ordering.
type sequential struct{}

func (r *sequential) String() string { return "sequential" }

func (r *sequential) List(p []proxy.IProxy) []proxy.IProxy {
	return p
}

var rn = rand.New(time.Now().UnixNano())
