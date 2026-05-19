package storage

import (
	"context"
	"testing"

	"github.com/ShizukaJiku/conduit/internal/config"
)

type nopStore struct{}

func (nopStore) Connect(context.Context) error       { return nil }
func (nopStore) Close() error                        { return nil }
func (nopStore) Ping() error                         { return nil }
func (nopStore) List() ([]FileInfo, []string, error) { return nil, nil, nil }
func (nopStore) Get(string, string) (int64, error)   { return 0, nil }
func (nopStore) Put(string, string) error            { return nil }
func (nopStore) Remove(string) error                 { return nil }
func (nopStore) EnsureDir(string) error              { return nil }
func (nopStore) RemoveDir(string) error              { return nil }

func reset() { drivers = map[string]Constructor{} }

func TestRegisterAndNew(t *testing.T) {
	reset()
	Register("x", func(*config.Config) (Storage, error) { return nopStore{}, nil })
	if got := Drivers(); len(got) != 1 || got[0] != "x" {
		t.Fatalf("Drivers = %v, want [x]", got)
	}
	if _, err := New(&config.Config{Backend: config.Backend{Type: "x"}}); err != nil {
		t.Fatalf("New(x): %v", err)
	}
}

func TestNewDefaultsToSFTP(t *testing.T) {
	reset()
	Register("sftp", func(*config.Config) (Storage, error) { return nopStore{}, nil })
	if _, err := New(&config.Config{}); err != nil { // empty Type → "sftp"
		t.Fatalf("empty backend type must default to sftp: %v", err)
	}
}

func TestNewUnknownErrors(t *testing.T) {
	reset()
	if _, err := New(&config.Config{Backend: config.Backend{Type: "nope"}}); err == nil {
		t.Error("unknown backend must error")
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	reset()
	Register("d", func(*config.Config) (Storage, error) { return nopStore{}, nil })
	defer func() {
		if recover() == nil {
			t.Error("duplicate Register must panic")
		}
	}()
	Register("d", func(*config.Config) (Storage, error) { return nopStore{}, nil })
}

func TestRegisterInvalidPanics(t *testing.T) {
	reset()
	for _, tc := range []struct {
		name string
		ctor Constructor
	}{
		{"", func(*config.Config) (Storage, error) { return nopStore{}, nil }},
		{"nilctor", nil},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Register(%q,...) must panic", tc.name)
				}
			}()
			Register(tc.name, tc.ctor)
		}()
	}
}
