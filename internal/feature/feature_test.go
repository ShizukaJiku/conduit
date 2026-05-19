package feature

import (
	"testing"

	"github.com/spf13/cobra"
)

type fake struct{ name string }

func (f fake) Name() string                   { return f.name }
func (f fake) Synopsis() string               { return "fake" }
func (f fake) NewCommand(Deps) *cobra.Command { return &cobra.Command{Use: f.name} }

func TestRegisterAndAllSorted(t *testing.T) {
	registry = map[string]Feature{}
	Register(fake{"watch"})
	Register(fake{"download"})

	got := All()
	if len(got) != 2 {
		t.Fatalf("want 2 features, got %d", len(got))
	}
	if got[0].Name() != "download" || got[1].Name() != "watch" {
		t.Fatalf("All() not sorted by name: %q, %q", got[0].Name(), got[1].Name())
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	registry = map[string]Feature{}
	Register(fake{"x"})
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	Register(fake{"x"})
}

func TestRegisterEmptyNamePanics(t *testing.T) {
	registry = map[string]Feature{}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on empty Name()")
		}
	}()
	Register(fake{""})
}
