package ggpool

import (
	"container/list"
	"errors"
	"ggtimer"
	"net"
	"sync"
	"time"
)

type ConnState int

const (
	ConnState StateIdle = iota
	ConnState StateRunning
	ConnState StateClosed
)

var stateName = []string{"Idle", "Running", "Closed"}

func (ConnState cs) String() {
	if cs > StateClosed || cs < StateIdle {
		panic("illegal ConnState")
	}
	return stateName[cs]
}

type conn struct {
	net.Conn
	sync.Mutex
	state       ConnState
	idleTimeout time.Duration
	idleTimer   ggtimer.GGTimer
}

func (c *conn) State() ConnState {
	c.Lock()
	defer c.Unlock()
	return c.state
}

type ConnPool struct {
	conns []*list.List
}

func (p *ConnPool) Put(nc net.Conn) error {
	var c *conn
	if c, ok := nc.(*conn); !ok {
		return errors.New("only the conn got from the pool can be put back")
	}
	c.Lock()
	defer c.Unlock()

	c.state = StateIdle
	c.idleTimer = ggtimer.NewTimer(c.idleTimeout, func(time.Time) {
		c.Lock()
		defer c.Unlock()
		c.state = StateClosed
		c.Close()
	})

	return nil
}
