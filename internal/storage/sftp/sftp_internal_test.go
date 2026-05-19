package sftp

import "testing"

func TestRemotePathBuilding(t *testing.T) {
	cases := []struct {
		root, rel, want string
	}{
		{"/srv/data", "a.txt", "/srv/data/a.txt"},
		{"/srv/data", "d/sub/b.txt", "/srv/data/d/sub/b.txt"},
		{"/srv/data", "", "/srv/data"},
		{"/srv/data", "../escape", "/srv/data/escape"}, // cleanRel strips traversal
		{"", "a.txt", "a.txt"},                         // empty root → relative
		{"", "d/sub/b.txt", "d/sub/b.txt"},
		{"", "", "."},
	}
	for _, c := range cases {
		d := &driver{root: c.root}
		if got := d.remote(c.rel); got != c.want {
			t.Errorf("remote(root=%q, rel=%q) = %q, want %q", c.root, c.rel, got, c.want)
		}
	}
}
