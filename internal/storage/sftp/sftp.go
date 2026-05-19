// Package sftp is the SFTP storage.Storage driver (conduit's v1 backend).
// Put/Get are atomic via a "<path>.part" temp + rename. Host-key
// verification (delta D1) uses known_hosts by default; the legacy
// accept-anything behavior requires explicit InsecureHostKey.
//
// The driver is safe for concurrent use: the engine (Steps 4/5) calls it
// from a watcher goroutine and a keepalive goroutine. d.sc/d.cli are
// guarded by mu; network I/O runs on a local snapshot, never under lock.
package sftp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/ShizukaJiku/conduit/internal/config"
	"github.com/ShizukaJiku/conduit/internal/storage"
)

func init() { storage.Register("sftp", New) }

const dialTimeout = 15 * time.Second

var errNotConnected = errors.New("sftp: not connected")

type driver struct {
	cfg  config.SFTPConfig
	root string // remote root, forward-slashed, no trailing slash

	mu  sync.RWMutex
	cli *ssh.Client
	sc  *sftp.Client
}

// New builds an SFTP driver from the resolved config.
func New(c *config.Config) (storage.Storage, error) {
	root := strings.TrimRight(filepath.ToSlash(c.RemoteFolder), "/")
	return &driver{cfg: c.Backend.SFTP, root: root}, nil
}

// snapshot returns the live sftp client under a read lock, or an error if
// the driver is not connected. Callers do I/O on the returned value
// without holding the lock (pkg/sftp clients are safe for concurrent use).
func (d *driver) snapshot() (*sftp.Client, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.sc == nil {
		return nil, errNotConnected
	}
	return d.sc, nil
}

func (d *driver) hostKeyCallback() (ssh.HostKeyCallback, error) {
	if d.cfg.InsecureHostKey {
		// D1: reproduces the legacy AutoAddPolicy. Step 7 adds the WARNING.
		return ssh.InsecureIgnoreHostKey(), nil
	}
	khPath := d.cfg.KnownHosts
	if khPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("sftp: home dir for known_hosts: %w", err)
		}
		khPath = filepath.Join(home, ".ssh", "known_hosts")
	}
	cb, err := knownhosts.New(khPath)
	if err != nil {
		return nil, fmt.Errorf("sftp: known_hosts %s: %w (use --insecure-host-key for the legacy behavior)", khPath, err)
	}
	return cb, nil
}

func (d *driver) Connect(ctx context.Context) error {
	hk, err := d.hostKeyCallback()
	if err != nil {
		return err
	}
	port := d.cfg.Port
	if port == 0 {
		port = 22
	}
	addr := net.JoinHostPort(d.cfg.Host, strconv.Itoa(port))

	sshCfg := &ssh.ClientConfig{
		User:            d.cfg.User,
		Auth:            []ssh.AuthMethod{ssh.Password(d.cfg.Password)},
		HostKeyCallback: hk,
		Timeout:         dialTimeout,
	}

	dialer := net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("sftp: dial %s: %w", addr, err)
	}
	// M1: ssh.NewClientConn ignores ClientConfig.Timeout on a pre-dialled
	// conn, so bound the handshake with the context deadline (or a default).
	deadline := time.Now().Add(dialTimeout)
	if dl, ok := ctx.Deadline(); ok {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)

	sconn, chans, reqs, err := ssh.NewClientConn(conn, addr, sshCfg)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("sftp: ssh handshake: %w", err)
	}
	cli := ssh.NewClient(sconn, chans, reqs)

	sc, err := sftp.NewClient(cli)
	if err != nil {
		_ = cli.Close()
		return fmt.Errorf("sftp: open subsystem: %w", err)
	}
	_ = conn.SetDeadline(time.Time{}) // clear: the session is long-lived

	// H1/H2: swap pointers under the lock and close any previous session
	// (a reconnect must not leak the old SSH/SFTP client).
	d.mu.Lock()
	oldSC, oldCli := d.sc, d.cli
	d.sc, d.cli = sc, cli
	d.mu.Unlock()
	if oldSC != nil {
		_ = oldSC.Close()
	}
	if oldCli != nil {
		_ = oldCli.Close()
	}
	return nil
}

func (d *driver) Close() error {
	d.mu.Lock()
	sc, cli := d.sc, d.cli
	d.sc, d.cli = nil, nil
	d.mu.Unlock()

	var first error
	if sc != nil {
		if err := sc.Close(); err != nil {
			first = err
		}
	}
	if cli != nil {
		if err := cli.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (d *driver) Ping() error {
	sc, err := d.snapshot()
	if err != nil {
		return err
	}
	root := d.root
	if root == "" {
		root = "." // relative to the server working directory
	}
	if _, err := sc.Stat(root); err != nil {
		return fmt.Errorf("sftp: ping %s: %w", root, err)
	}
	return nil
}

func cleanRel(rel string) string {
	return strings.Trim(path.Clean("/"+strings.ReplaceAll(rel, "\\", "/")), "/")
}

func (d *driver) remote(rel string) string {
	rel = cleanRel(rel)
	if d.root == "" { // relative to the server working directory
		if rel == "" {
			return "."
		}
		return rel
	}
	if rel == "" {
		return d.root
	}
	return d.root + "/" + rel
}

func (d *driver) List() ([]storage.FileInfo, []string, error) {
	sc, err := d.snapshot()
	if err != nil {
		return nil, nil, err
	}
	root := d.root
	if root == "" {
		root = "." // relative to the server working directory
	}
	var files []storage.FileInfo
	var dirs []string
	var walk func(dir, rel string) error
	walk = func(dir, rel string) error {
		entries, err := sc.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("sftp: readdir %s: %w", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if name == "." || name == ".." {
				continue
			}
			childRel := name
			if rel != "" {
				childRel = rel + "/" + name
			}
			full := name
			if dir != "." {
				full = dir + "/" + name
			}
			if e.IsDir() {
				dirs = append(dirs, childRel)
				if err := walk(full, childRel); err != nil {
					return err
				}
				continue
			}
			files = append(files, storage.FileInfo{
				Path:        childRel,
				Size:        e.Size(),
				ModTimeUnix: e.ModTime().Unix(),
			})
		}
		return nil
	}
	if err := walk(root, ""); err != nil {
		return nil, nil, err
	}
	return files, dirs, nil
}

func (d *driver) Put(localPath, remoteRel string) error {
	sc, err := d.snapshot()
	if err != nil {
		return err
	}
	rem := d.remote(remoteRel)
	if dir := path.Dir(rem); dir != "." && dir != "/" {
		if err := sc.MkdirAll(dir); err != nil {
			return fmt.Errorf("sftp: mkdir %s: %w", dir, err)
		}
	}
	src, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer src.Close()

	tmp := rem + ".part"
	dst, err := sc.Create(tmp)
	if err != nil {
		return fmt.Errorf("sftp: create %s: %w", tmp, err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = sc.Remove(tmp)
		return fmt.Errorf("sftp: upload %s: %w", rem, err)
	}
	if err := dst.Close(); err != nil {
		_ = sc.Remove(tmp)
		return fmt.Errorf("sftp: close %s: %w", tmp, err)
	}
	// Prefer the atomic POSIX rename; fall back to remove+rename for
	// servers that do not advertise posix-rename@openssh.com (M3).
	if err := sc.PosixRename(tmp, rem); err != nil {
		_ = sc.Remove(rem)
		if rerr := sc.Rename(tmp, rem); rerr != nil {
			_ = sc.Remove(tmp)
			return fmt.Errorf("sftp: rename %s: %w", rem, rerr)
		}
	}
	return nil
}

func (d *driver) Get(remoteRel, localPath string) (int64, error) {
	sc, err := d.snapshot()
	if err != nil {
		return 0, err
	}
	rem := d.remote(remoteRel)
	src, err := sc.Open(rem)
	if err != nil {
		return 0, fmt.Errorf("sftp: open %s: %w", rem, err)
	}
	defer src.Close()

	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return 0, err
	}
	tmp := localPath + ".part"
	dst, err := os.Create(tmp)
	if err != nil {
		return 0, err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("sftp: download %s: %w", rem, err)
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	// M4: stat the open handle AFTER the copy so mtime and content refer
	// to the same version (no Stat-before-download TOCTOU).
	fi, err := src.Stat()
	if err != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("sftp: stat %s: %w", rem, err)
	}
	if err := os.Rename(tmp, localPath); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return fi.ModTime().Unix(), nil
}

func (d *driver) Remove(remoteRel string) error {
	sc, err := d.snapshot()
	if err != nil {
		return err
	}
	if err := sc.Remove(d.remote(remoteRel)); err != nil {
		return fmt.Errorf("sftp: remove %s: %w", remoteRel, err)
	}
	return nil
}

func (d *driver) EnsureDir(remoteRel string) error {
	sc, err := d.snapshot()
	if err != nil {
		return err
	}
	rel := cleanRel(remoteRel)
	if rel == "" {
		return nil
	}
	if err := sc.MkdirAll(d.remote(rel)); err != nil {
		return fmt.Errorf("sftp: mkdir %s: %w", rel, err)
	}
	return nil
}

func (d *driver) RemoveDir(remoteRel string) error {
	sc, err := d.snapshot()
	if err != nil {
		return err
	}
	rel := cleanRel(remoteRel)
	if rel == "" {
		return errors.New("sftp: refusing to remove root")
	}
	if err := sc.RemoveDirectory(d.remote(rel)); err != nil {
		return fmt.Errorf("sftp: rmdir %s: %w", rel, err)
	}
	return nil
}
