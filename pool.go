package ggpool

import (
	"container/list"
	"fmt"
	"ggtimer"
	"net"
	"sync"
	"time"
)

type ConnState int

const (
	StateIdle ConnState = iota
	StateRunning
	StateClosed
)

var stateName = []string{"Idle", "Running", "Closed"}

func (cs ConnState) String() string {
	if cs > StateClosed || cs < StateIdle {
		panic("illegal ConnState")
	}
	return stateName[cs]
}

type GGConn struct {
	sync.Mutex
	*net.TCPConn
	state     ConnState
	idleTimer ggtimer.GGTimer
	ready     chan bool
}

func (c *GGConn) State() ConnState {
	c.Lock()
	defer c.Unlock()
	return c.state
}

func (c *GGConn) lockedSetState(s ConnState) {
	c.state = s
}

func (c *GGConn) lockedState() ConnState {
	return c.state
}

func (c *GGConn) SetState(s ConnState) {
	c.Lock()
	defer c.Unlock()
	if s > StateClosed || s < StateIdle {
		panic("illegal state")
	}
	c.state = s
}

func NewGGConn(addr *net.TCPAddr) (*GGConn, error) {
	tc, err := net.DialTCP("tcp", nil, addr)
	if err != nil {
		return nil, err
	}

	return &GGConn{
		TCPConn:   tc,
		idleTimer: nil,
		ready:     make(chan bool, 1),
	}, nil
}

type GGConnPool struct {
	sync.Mutex  // guard conns
	conns       map[string]*list.List
	idleTimeout time.Duration
}

func NewGGConnPool(d time.Duration) *GGConnPool {
	return &GGConnPool{
		conns:       make(map[string]*list.List),
		idleTimeout: d,
	}
}

func (p *GGConnPool) Get(target string) (ggc *GGConn, err error) {
	var tcpAddr *net.TCPAddr
	if tcpAddr, err = net.ResolveTCPAddr("tcp", target); err != nil {
		return
	}

	p.Lock()
	defer p.Unlock()

	//no connection to target before, new a new one
	connList, ok := p.conns[tcpAddr.String()]
	if !ok {
		if ggc, err = NewGGConn(tcpAddr); err != nil {
			return
		}

		// it's a new connection, no need to require a lock on it
		ggc.lockedSetState(StateRunning)
		fmt.Println("new connection")
		return
	}

	//try to get a connection from before
	var next *list.Element
	for itr := connList.Front(); itr != connList.Back(); itr = next {
		next = itr.Next()
		ggc = itr.Value.(*GGConn)
		ggc.Lock()

		// find an idle connection, remove it from list and return this connection
		if ggc.lockedState() == StateIdle {
			defer ggc.Unlock()

			// stop the read goroutine
			ggc.SetDeadline(time.Now())
			<-ggc.ready

			ggc.lockedSetState(StateRunning)
			ggc.idleTimer.Close()
			connList.Remove(itr)
			fmt.Println("cached connection")
			return
		}

		if ggc.lockedState() != StateClosed {
			panic("only closed and idle connection will be hold in pool")
		}

		// find a closed one, just remove it
		ggc.idleTimer.Close()
		connList.Remove(itr)
		ggc.Unlock()
	}

	// no idle connection found
	if ggc, err = NewGGConn(tcpAddr); err != nil {
		return
	}
	ggc.lockedSetState(StateRunning)
	fmt.Println("new connection")
	return
}

func (p *GGConnPool) Put(ggc *GGConn) error {
	addr := ggc.RemoteAddr().String()

	// since this connection is going to be put back to the pool, it should be in StateRunning now
	// so the idleTimer is not on, no other goroutine will get access to ggc, no need to lock ggc
	ggc.lockedSetState(StateIdle)
	ggc.idleTimer = ggtimer.NewTimer(p.idleTimeout, func(time.Time) {
		// a closed connection will not be used again, so, no need to hold the lock
		ggc.SetState(StateClosed)
		ggc.idleTimer.Close()
		ggc.Close()
	})

	go func() {
		ggc.SetDeadline(time.Time{})
		_, err := ggc.Read(make([]byte, 1))
		if nerr, ok := err.(net.Error); (!ok || !nerr.Timeout()) && ggc.State() != StateClosed {
			ggc.SetState(StateClosed)
			ggc.idleTimer.Close()
			ggc.Close()
		}
		ggc.ready <- true
	}()

	// wait the ggc.Read(make([]byte, 1)) to block
	// any better way?
	time.Sleep(0)

	p.Lock()
	defer p.Unlock()

	if p.conns[addr] == nil {
		p.conns[addr] = list.New()
	}

	p.conns[addr].PushBack(ggc)

	return nil
}
