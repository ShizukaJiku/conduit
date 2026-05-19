package sftp_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/ShizukaJiku/conduit/internal/config"
	"github.com/ShizukaJiku/conduit/internal/storage"
	_ "github.com/ShizukaJiku/conduit/internal/storage/sftp"
	"github.com/ShizukaJiku/conduit/testutil/sftpserver"
	"github.com/ShizukaJiku/conduit/testutil/storagetest"
)

func hostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		t.Fatal(err)
	}
	return h, n
}

func newCfg(t *testing.T, srv *sftpserver.Server, insecure bool, knownHosts string) *config.Config {
	host, port := hostPort(t, srv.Addr)
	return &config.Config{
		RemoteFolder: filepath.ToSlash(srv.Root),
		Backend: config.Backend{
			Type: "sftp",
			SFTP: config.SFTPConfig{
				Host:            host,
				Port:            port,
				User:            srv.User,
				Password:        srv.Pass,
				InsecureHostKey: insecure,
				KnownHosts:      knownHosts,
			},
		},
	}
}

func TestSFTPContract(t *testing.T) {
	storagetest.RunContract(t, func(t *testing.T) storage.Storage {
		srv := sftpserver.Start(t)
		s, err := storage.New(newCfg(t, srv, true, ""))
		if err != nil {
			t.Fatalf("storage.New(sftp): %v", err)
		}
		return s
	})
}

func writeKnownHosts(t *testing.T, srv *sftpserver.Server) string {
	t.Helper()
	line := knownhosts.Line([]string{knownhosts.Normalize(srv.Addr)}, srv.HostKey)
	p := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(p, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHostKeyAcceptedFromKnownHosts(t *testing.T) {
	srv := sftpserver.Start(t)
	s, err := storage.New(newCfg(t, srv, false, writeKnownHosts(t, srv)))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Connect(context.Background()); err != nil {
		t.Fatalf("Connect with matching known_hosts must succeed: %v", err)
	}
	_ = s.Close()
}

func TestHostKeyUnknownRejected(t *testing.T) {
	srv := sftpserver.Start(t)
	empty := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := storage.New(newCfg(t, srv, false, empty))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Connect(context.Background()); err == nil {
		_ = s.Close()
		t.Fatal("Connect must fail for an unknown host key (MITM protection)")
	}
}

func TestKnownHostsFileMissingErrors(t *testing.T) {
	srv := sftpserver.Start(t)
	s, err := storage.New(newCfg(t, srv, false, filepath.Join(t.TempDir(), "nope")))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Connect(context.Background()); err == nil {
		_ = s.Close()
		t.Fatal("missing known_hosts file must error (not silently accept)")
	}
}

func TestDialFailure(t *testing.T) {
	// Grab a port then close it → guaranteed connection refused, fast.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	host, port := hostPort(t, addr)

	s, err := storage.New(&config.Config{
		Backend: config.Backend{Type: "sftp", SFTP: config.SFTPConfig{
			Host: host, Port: port, User: "x", Password: "y", InsecureHostKey: true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Connect(context.Background()); err == nil {
		_ = s.Close()
		t.Fatal("Connect to a closed port must fail")
	}
}

func TestReconnect(t *testing.T) {
	srv := sftpserver.Start(t)
	s, err := storage.New(newCfg(t, srv, true, ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Ping(); err != nil {
		t.Fatalf("Ping after connect: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Connect(context.Background()); err != nil {
		t.Fatalf("reconnect must succeed: %v", err)
	}
	if err := s.Ping(); err != nil {
		t.Fatalf("Ping after reconnect: %v", err)
	}
	_ = s.Close()
}
