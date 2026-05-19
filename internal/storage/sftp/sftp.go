// Package sftp is the SFTP storage.Storage driver (conduit's v1 backend).
// Put/Get are atomic via a "<path>.part" temp + PosixRename. Host-key
// verification (delta D1) uses known_hosts by default; the legacy
// accept-anything behavior requires explicit InsecureHostKey.
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
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/ShizukaJiku/conduit/internal/config"
	"github.com/ShizukaJiku/conduit/internal/storage"
)

func init() { storage.Register("sftp", New) }

const dialTimeout = 15 * time.Second

type driver struct {
	cfg  config.SFTPConfig
	root string // remote root, forward-slashed, no trailing slash
	cli  *ssh.Client
	sc   *sftp.Client
}

// New builds an SFTP driver from the resolved config.
func New(c *config.Config) (storage.Storage, error) {
	root := strings.TrimRight(filepath.ToSlash(c.RemoteFolder), "/")
	return &driver{cfg: c.Backend.SFTP, root: root}, nil
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
		return nil, fmt.Errorf("sftp: known_hosts %s: %w (usá --insecure-host-key para el comportamiento legacy)", khPath, err)
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
	addr := net.JoinHostPort(d.cfg.Host, fmt.Sprintf("%d", port))

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
	sconn, chans, reqs, err := ssh.NewClientConn(conn, addr, sshCfg)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("sftp: ssh handshake: %w", err)
	}
	d.cli = ssh.NewClient(sconn, chans, reqs)

	sc, err := sftp.NewClient(d.cli)
	if err != nil {
		_ = d.cli.Close()
		d.cli = nil
		return fmt.Errorf("sftp: open subsystem: %w", err)
	}
	d.sc = sc
	return nil
}

func (d *driver) Close() error {
	var first error
	if d.sc != nil {
		if err := d.sc.Close(); err != nil {
			first = err
		}
		d.sc = nil
	}
	if d.cli != nil {
		if err := d.cli.Close(); err != nil && first == nil {
			first = err
		}
		d.cli = nil
	}
	return first
}

func (d *driver) Ping() error {
	if d.sc == nil {
		return errors.New("sftp: not connected")
	}
	root := d.root
	if root == "" {
		root = "/"
	}
	_, err := d.sc.Stat(root)
	return err
}

func cleanRel(rel string) string {
	return strings.Trim(path.Clean("/"+strings.ReplaceAll(rel, "\\", "/")), "/")
}

func (d *driver) remote(rel string) string {
	rel = cleanRel(rel)
	root := d.root
	if root == "" {
		root = "."
	}
	if rel == "" {
		return root
	}
	return root + "/" + rel
}

func (d *driver) List() ([]storage.FileInfo, []string, error) {
	if d.sc == nil {
		return nil, nil, errors.New("sftp: not connected")
	}
	root := d.root
	if root == "" {
		root = "."
	}
	var files []storage.FileInfo
	var dirs []string
	var walk func(dir, rel string) error
	walk = func(dir, rel string) error {
		entries, err := d.sc.ReadDir(dir)
		if err != nil {
			return err
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
			full := dir + "/" + name
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
	if d.sc == nil {
		return errors.New("sftp: not connected")
	}
	rem := d.remote(remoteRel)
	if dir := path.Dir(rem); dir != "." && dir != "/" {
		if err := d.sc.MkdirAll(dir); err != nil {
			return fmt.Errorf("sftp: mkdir %s: %w", dir, err)
		}
	}
	src, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer src.Close()

	tmp := rem + ".part"
	dst, err := d.sc.Create(tmp)
	if err != nil {
		return fmt.Errorf("sftp: create %s: %w", tmp, err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = d.sc.Remove(tmp)
		return fmt.Errorf("sftp: upload %s: %w", rem, err)
	}
	if err := dst.Close(); err != nil {
		_ = d.sc.Remove(tmp)
		return err
	}
	if err := d.sc.PosixRename(tmp, rem); err != nil {
		_ = d.sc.Remove(tmp)
		return fmt.Errorf("sftp: rename %s: %w", rem, err)
	}
	return nil
}

func (d *driver) Get(remoteRel, localPath string) (int64, error) {
	if d.sc == nil {
		return 0, errors.New("sftp: not connected")
	}
	rem := d.remote(remoteRel)
	fi, err := d.sc.Stat(rem)
	if err != nil {
		return 0, fmt.Errorf("sftp: stat %s: %w", rem, err)
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return 0, err
	}
	src, err := d.sc.Open(rem)
	if err != nil {
		return 0, fmt.Errorf("sftp: open %s: %w", rem, err)
	}
	defer src.Close()

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
	if err := os.Rename(tmp, localPath); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return fi.ModTime().Unix(), nil
}

func (d *driver) Remove(remoteRel string) error {
	if d.sc == nil {
		return errors.New("sftp: not connected")
	}
	return d.sc.Remove(d.remote(remoteRel))
}

func (d *driver) EnsureDir(remoteRel string) error {
	if d.sc == nil {
		return errors.New("sftp: not connected")
	}
	rel := cleanRel(remoteRel)
	if rel == "" {
		return nil
	}
	return d.sc.MkdirAll(d.remote(rel))
}

func (d *driver) RemoveDir(remoteRel string) error {
	if d.sc == nil {
		return errors.New("sftp: not connected")
	}
	rel := cleanRel(remoteRel)
	if rel == "" {
		return errors.New("sftp: refusing to remove root")
	}
	return d.sc.RemoveDirectory(d.remote(rel))
}
