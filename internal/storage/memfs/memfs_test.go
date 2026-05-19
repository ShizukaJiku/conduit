package memfs_test

import (
	"testing"

	"github.com/ShizukaJiku/conduit/internal/config"
	"github.com/ShizukaJiku/conduit/internal/storage"
	_ "github.com/ShizukaJiku/conduit/internal/storage/memfs"
	"github.com/ShizukaJiku/conduit/testutil/storagetest"
)

func TestMemfsContract(t *testing.T) {
	storagetest.RunContract(t, func(t *testing.T) storage.Storage {
		s, err := storage.New(&config.Config{Backend: config.Backend{Type: "memfs"}})
		if err != nil {
			t.Fatalf("storage.New(memfs): %v", err)
		}
		return s
	})
}
