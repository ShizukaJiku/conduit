// Package sftpserver is an in-process SSH+SFTP server for hermetic tests
// (no Docker). It serves a temp directory over the real filesystem with
// password auth and an ephemeral host key.
package sftpserver

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Server is a running test SFTP server. Stop is registered with t.Cleanup.
type Server struct {
	Addr    string        // host:port, e.g. 127.0.0.1:54321
	Root    string        // absolute temp dir served (forward-slashed by callers)
	User    string        // accepted username
	Pass    string        // accepted password
	HostKey ssh.PublicKey // for building a known_hosts entry
}

// Start launches the server on a random localhost port serving t.TempDir().
func Start(t *testing.T) *Server {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("sftpserver: keygen: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("sftpserver: signer: %v", err)
	}

	const user, pass = "tester", "s3cret"
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			if c.User() == user && string(p) == pass {
				return nil, nil
			}
			return nil, errAuth
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("sftpserver: listen: %v", err)
	}

	s := &Server{
		Addr:    ln.Addr().String(),
		Root:    t.TempDir(),
		User:    user,
		Pass:    pass,
		HostKey: signer.PublicKey(),
	}

	go acceptLoop(ln, cfg)
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

var errAuth = &authError{}

type authError struct{}

func (*authError) Error() string { return "sftpserver: auth failed" }

func acceptLoop(ln net.Listener, cfg *ssh.ServerConfig) {
	for {
		nConn, err := ln.Accept()
		if err != nil {
			return // listener closed → shutdown
		}
		go handleConn(nConn, cfg)
	}
}

func handleConn(nConn net.Conn, cfg *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(nConn, cfg)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)

	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "only session")
			continue
		}
		ch, requests, err := nc.Accept()
		if err != nil {
			return
		}
		go func(in <-chan *ssh.Request) {
			for req := range in {
				ok := req.Type == "subsystem" && len(req.Payload) >= 4 &&
					string(req.Payload[4:]) == "sftp"
				_ = req.Reply(ok, nil)
			}
		}(requests)

		srv, err := sftp.NewServer(ch)
		if err != nil {
			_ = ch.Close()
			continue
		}
		go func() {
			_ = srv.Serve()
			_ = ch.Close()
		}()
	}
}
