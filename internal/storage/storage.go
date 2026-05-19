// Package storage defines conduit's backend-agnostic Storage contract and
// a driver registry. Engines (sync/download) operate only against the
// Storage interface; concrete drivers (memfs, sftp, s3) self-register from
// their package init() — the database/sql pattern, which also breaks the
// import cycle (storage ← driver, never storage → driver).
//
// Atomicity of Put/Get, host-key verification and "directory" semantics
// are driver implementation details, never exposed by this interface.
package storage

import (
	"context"
	"fmt"
	"sort"

	"github.com/ShizukaJiku/conduit/internal/config"
)

// FileInfo describes a remote file. ModTimeUnix is seconds (truncated) to
// preserve the legacy ±2s integer-second mtime parity.
type FileInfo struct {
	Path        string // relative to the configured remote root, "/" separator
	Size        int64
	ModTimeUnix int64
}

// Storage is the contract every backend driver implements.
type Storage interface {
	Connect(ctx context.Context) error
	Close() error
	Ping() error // keepalive; an error makes the engine reconnect

	List() (files []FileInfo, dirs []string, err error) // recursive from root

	Get(remoteRel, localPath string) (modTimeUnix int64, err error) // atomic
	Put(localPath, remoteRel string) error                          // atomic
	Remove(remoteRel string) error

	EnsureDir(remoteRel string) error // object stores: no-op
	RemoveDir(remoteRel string) error // object stores: no-op
}

// Constructor builds a Storage for the given resolved config.
type Constructor func(cfg *config.Config) (Storage, error)

// Factory is the dependency handed to features (see internal/feature.Deps).
type Factory = Constructor

var drivers = map[string]Constructor{}

// Register adds a driver under name. Panics on empty or duplicate name so
// collisions fail at process start, not silently at runtime.
func Register(name string, c Constructor) {
	if name == "" {
		panic("storage: Register with empty name")
	}
	if c == nil {
		panic("storage: Register " + name + " with nil constructor")
	}
	if _, dup := drivers[name]; dup {
		panic("storage: duplicate driver " + name)
	}
	drivers[name] = c
}

// Drivers returns the registered driver names, sorted.
func Drivers() []string {
	out := make([]string, 0, len(drivers))
	for n := range drivers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// New builds the Storage selected by cfg.Backend.Type (default "sftp").
func New(cfg *config.Config) (Storage, error) {
	name := cfg.Backend.Type
	if name == "" {
		name = "sftp"
	}
	c, ok := drivers[name]
	if !ok {
		return nil, fmt.Errorf("storage: backend %q no registrado (disponibles: %v)", name, Drivers())
	}
	return c(cfg)
}
