package ingress

import (
	"errors"
	"net"
	"sync"
)

// Listener rejects excess connections immediately after Accept, before parsing.
// Close stops admission; existing connections retain their leases until closed.
type Listener struct {
	net.Listener
	OnReject    func()
	mu          sync.Mutex
	maximum     int
	closed      bool
	connections map[*leasedConn]struct{}
}

func NewListener(inner net.Listener, maximum int) (*Listener, error) {
	if inner == nil {
		return nil, errors.New("nil ingress listener")
	}
	if err := (Limits{MaxConnections: maximum}).Validate(); err != nil {
		return nil, err
	}
	return &Listener{Listener: inner, maximum: maximum, connections: make(map[*leasedConn]struct{})}, nil
}

func (l *Listener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			_ = conn.Close()
			return nil, net.ErrClosed
		}
		if l.maximum > 0 && len(l.connections) >= l.maximum {
			l.mu.Unlock()
			_ = conn.Close()
			if l.OnReject != nil {
				l.OnReject()
			}
			continue
		}
		leased := &leasedConn{Conn: conn, owner: l}
		l.connections[leased] = struct{}{}
		l.mu.Unlock()
		return leased, nil
	}
}

func (l *Listener) Close() error {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	return l.Listener.Close()
}

// CloseConnections is the forced-shutdown path, including connections which
// have not completed HTTP/2 handshaking and are not yet registered by gRPC.
func (l *Listener) CloseConnections() error {
	_ = l.Close()
	l.mu.Lock()
	connections := make([]*leasedConn, 0, len(l.connections))
	for conn := range l.connections {
		connections = append(connections, conn)
	}
	l.mu.Unlock()
	var result error
	for _, conn := range connections {
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			result = errors.Join(result, err)
		}
	}
	return result
}

type leasedConn struct {
	net.Conn
	owner    *Listener
	once     sync.Once
	closeErr error
}

func (c *leasedConn) Close() error {
	c.once.Do(func() {
		c.closeErr = c.Conn.Close()
		c.owner.mu.Lock()
		delete(c.owner.connections, c)
		c.owner.mu.Unlock()
	})
	return c.closeErr
}
