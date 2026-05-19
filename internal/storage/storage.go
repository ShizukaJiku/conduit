// Package storage defines conduit's backend-agnostic Storage contract.
// Engines (sync/download) operate only against this interface; concrete
// drivers (memfs, sftp, s3) are implemented in later steps. Atomicity of
// Put/Get, host-key verification and "directory" semantics are driver
// implementation details, never exposed here.
package storage

import (
	"context"

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

// Factory builds a Storage for the configured backend. The concrete
// selection (sftp/s3/...) is implemented in the storage-core step.
type Factory func(cfg *config.Config) (Storage, error)
