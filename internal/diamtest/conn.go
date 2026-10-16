// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

// Package diamtest provides an in-memory diam.Conn for tests. Only test code
// imports it.
package diamtest

import (
	"bytes"
	"context"
	"crypto/tls"
	"net"
	"sync"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// Conn records what is written to it. Each Conn is a distinct connection:
// compare them by pointer.
type Conn struct {
	Remote string // remote IP; defaults to 192.0.2.1

	mu      sync.Mutex
	written [][]byte
	ctx     context.Context
}

var _ diam.Conn = (*Conn)(nil)

func (c *Conn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.written = append(c.written, append([]byte(nil), b...))
	return len(b), nil
}

func (c *Conn) WriteStream(b []byte, _ uint) (int, error) { return c.Write(b) }
func (c *Conn) Close()                                    {}
func (c *Conn) LocalAddr() net.Addr                       { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 3868} }
func (c *Conn) TLS() *tls.ConnectionState                 { return nil }
func (c *Conn) Dictionary() *dict.Parser                  { return dict.Default }
func (c *Conn) Connection() net.Conn                      { return nil }

func (c *Conn) RemoteAddr() net.Addr {
	ip := net.ParseIP(c.Remote)
	if ip == nil {
		ip = net.IPv4(192, 0, 2, 1)
	}
	return &net.TCPAddr{IP: ip, Port: 3868}
}

func (c *Conn) Context() context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}

func (c *Conn) SetContext(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ctx = ctx
}

// Messages decodes everything written so far.
func (c *Conn) Messages() ([]*diam.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*diam.Message, 0, len(c.written))
	for _, b := range c.written {
		m, err := diam.ReadMessage(bytes.NewReader(b), dict.Default)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}
