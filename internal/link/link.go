// Package link implements a framed, multiplexed TCP request/response link
// between the gateway and a world process.
package link

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxFrame     = 4 << 20
	frameRequest = byte(1)
	frameReply   = byte(2)
	frameError   = byte(3)
	framePing    = byte(4)
	framePong    = byte(5)
	framePush    = byte(6)
)

// ErrClosed means the link is closed.
var ErrClosed = errors.New("link closed")

// Handler serves one request payload.
type Handler func(ctx context.Context, payload []byte) ([]byte, error)

// Client is one multiplexed connection to a world.
type Client struct {
	conn net.Conn
	bw   *bufio.Writer
	wmu  sync.Mutex

	mu      sync.Mutex
	nextID  uint64
	pending map[uint64]chan result
	done    chan struct{}
	once    sync.Once
	onPush  func([]byte)
}

type result struct {
	data []byte
	err  error
}

// Dial opens a client link.
func Dial(addr string, timeout time.Duration) (*Client, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	c := &Client{
		conn:    conn,
		bw:      bufio.NewWriter(conn),
		pending: make(map[uint64]chan result),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

// Call sends one request and waits for its correlated reply.
func (c *Client) Call(ctx context.Context, payload []byte) ([]byte, error) {
	id := atomic.AddUint64(&c.nextID, 1)
	ch := make(chan result, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.write(frameRequest, id, payload); err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		return r.data, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, ErrClosed
	}
}

func (c *Client) write(typ byte, id uint64, payload []byte) error {
	var hdr [13]byte
	binary.BigEndian.PutUint32(hdr[0:4], uint32(9+len(payload)))
	hdr[4] = typ
	binary.BigEndian.PutUint64(hdr[5:13], id)
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := c.bw.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := c.bw.Write(payload); err != nil {
		return err
	}
	return c.bw.Flush()
}

func (c *Client) readLoop() {
	defer func() { _ = c.Close() }()
	br := bufio.NewReader(c.conn)
	for {
		typ, id, payload, err := readFrame(br)
		if err != nil {
			return
		}
		switch typ {
		case frameReply, frameError:
			c.mu.Lock()
			ch := c.pending[id]
			c.mu.Unlock()
			if ch == nil {
				continue
			}
			if typ == frameError {
				ch <- result{err: errors.New(string(payload))}
			} else {
				ch <- result{data: payload}
			}
		case framePush:
			c.mu.Lock()
			h := c.onPush
			c.mu.Unlock()
			if h != nil {
				h(payload)
			}
		case framePing:
			_ = c.write(framePong, id, nil)
		}
	}
}

// SetPushHandler installs a callback for server-initiated push frames.
func (c *Client) SetPushHandler(h func([]byte)) {
	c.mu.Lock()
	c.onPush = h
	c.mu.Unlock()
}

// Close shuts the connection and fails pending calls.
func (c *Client) Close() error {
	c.once.Do(func() {
		close(c.done)
		_ = c.conn.Close()
		c.mu.Lock()
		for id, ch := range c.pending {
			ch <- result{err: ErrClosed}
			delete(c.pending, id)
		}
		c.mu.Unlock()
	})
	return nil
}

// Server accepts links and dispatches requests to a Handler.
type Server struct {
	ln      net.Listener
	handler Handler
	sem     chan struct{}
}

// Listen starts a TCP listener.
func Listen(addr string, h Handler) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &Server{ln: ln, handler: h, sem: make(chan struct{}, 1024)}, nil
}

// Addr reports the bound address.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Serve accepts until the listener is closed.
func (s *Server) Serve(ctx context.Context) error {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return err
		}
		go s.serveConn(ctx, conn)
	}
}

// Close stops the listener.
func (s *Server) Close() error { return s.ln.Close() }

func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)
	bw := bufio.NewWriter(conn)
	var wmu sync.Mutex
	_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))

	for {
		typ, id, payload, err := readFrame(br)
		if err != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		switch typ {
		case framePing:
			wmu.Lock()
			werr := writeFrame(bw, framePong, id, nil)
			wmu.Unlock()
			if werr != nil {
				return
			}
		case frameRequest:
			select {
			case s.sem <- struct{}{}:
			default:
				wmu.Lock()
				_ = writeFrame(bw, frameError, id, []byte("server busy"))
				wmu.Unlock()
				continue
			}
			go func(id uint64, payload []byte) {
				defer func() { <-s.sem }()
				data, herr := s.handler(ctx, payload)
				wmu.Lock()
				defer wmu.Unlock()
				if herr != nil {
					_ = writeFrame(bw, frameError, id, []byte(herr.Error()))
					return
				}
				_ = writeFrame(bw, frameReply, id, data)
			}(id, payload)
		}
	}
}

func readFrame(br *bufio.Reader) (byte, uint64, []byte, error) {
	var l [4]byte
	if _, err := io.ReadFull(br, l[:]); err != nil {
		return 0, 0, nil, err
	}
	n := binary.BigEndian.Uint32(l[:])
	if n < 9 || n > maxFrame {
		return 0, 0, nil, fmt.Errorf("bad frame length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(br, buf); err != nil {
		return 0, 0, nil, err
	}
	typ := buf[0]
	id := binary.BigEndian.Uint64(buf[1:9])
	return typ, id, buf[9:], nil
}

func writeFrame(bw *bufio.Writer, typ byte, id uint64, payload []byte) error {
	var hdr [13]byte
	binary.BigEndian.PutUint32(hdr[0:4], uint32(9+len(payload)))
	hdr[4] = typ
	binary.BigEndian.PutUint64(hdr[5:13], id)
	if _, err := bw.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := bw.Write(payload); err != nil {
		return err
	}
	return bw.Flush()
}
