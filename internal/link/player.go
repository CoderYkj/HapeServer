package link

import (
	"bufio"
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

// Conn is one player connection with explicit reply/push writes.
type Conn struct {
	conn net.Conn
	br   *bufio.Reader
	bw   *bufio.Writer
	wmu  sync.Mutex
}

// Read waits for the next request frame.
func (c *Conn) Read() (uint64, []byte, error) {
	_ = c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	typ, id, payload, err := readFrame(c.br)
	if err != nil {
		return 0, nil, err
	}
	if typ != frameRequest {
		return 0, nil, errors.New("expected request frame")
	}
	return id, payload, nil
}

// Reply sends a response for one request id.
func (c *Conn) Reply(id uint64, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return writeFrame(c.bw, frameReply, id, payload)
}

// Push sends a server-initiated event.
func (c *Conn) Push(payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return writeFrame(c.bw, framePush, 0, payload)
}

// Close closes the underlying connection.
func (c *Conn) Close() error { return c.conn.Close() }

// ConnHandler serves one player connection.
type ConnHandler func(ctx context.Context, c *Conn)

// PlayerServer accepts player connections and hands them to a ConnHandler.
type PlayerServer struct {
	ln      net.Listener
	handler ConnHandler
}

// ListenPlayer starts a player TCP listener.
func ListenPlayer(addr string, h ConnHandler) (*PlayerServer, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &PlayerServer{ln: ln, handler: h}, nil
}

// Addr reports the bound address.
func (s *PlayerServer) Addr() string { return s.ln.Addr().String() }

// Serve accepts until the listener closes.
func (s *PlayerServer) Serve(ctx context.Context) error {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return err
		}
		go func() {
			c := &Conn{conn: conn, br: bufio.NewReader(conn), bw: bufio.NewWriter(conn)}
			defer func() { _ = c.Close() }()
			s.handler(ctx, c)
		}()
	}
}

// Close stops the listener.
func (s *PlayerServer) Close() error { return s.ln.Close() }
