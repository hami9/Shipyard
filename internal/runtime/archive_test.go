package runtime

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

type entry struct {
	name, body string
	dir        bool
	uid        int
}

func archive(t *testing.T, entries ...entry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o600, Size: int64(len(e.body)), Uid: e.uid, Gid: e.uid, Uname: "caddy", Gname: "caddy"}
		if e.dir {
			h.Typeflag, h.Mode = tar.TypeDir, 0o700
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(e.body))
	}
	tw.Close()
	return &buf
}

// P3.6b: a restore re-roots the archive of /data at the volume itself and
// makes every entry root's, keeping modes and contents.
func TestRerootArchive(t *testing.T) {
	in := archive(t, entry{name: "data/", dir: true}, entry{name: "data/caddy/", dir: true, uid: 1000},
		entry{name: "data/caddy/certificates/a.key", body: "private", uid: 1000})
	var out bytes.Buffer
	if err := rerootArchive(in, &out); err != nil {
		t.Fatal(err)
	}
	var got []string
	tr := tar.NewReader(&out)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		got = append(got, fmt.Sprintf("%s %o %d:%d %q%q %q", h.Name, h.Mode, h.Uid, h.Gid, h.Uname, h.Gname, body))
	}
	want := []string{`caddy/ 700 0:0 """" ""`, `caddy/certificates/a.key 600 0:0 """" "private"`}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("entries =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// An entry outside data/ stops the restore: the archive is not one that
// ArchiveEdgeData wrote.
func TestRerootArchiveRefuses(t *testing.T) {
	for _, name := range []string{"etc/passwd", "data/../etc/passwd", "/data/caddy/x", "../data/x", "database.dump"} {
		err := rerootArchive(archive(t, entry{name: "data/ok", body: "x"}, entry{name: name, body: "x"}), io.Discard)
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "outside data/") {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
	if err := rerootArchive(strings.NewReader("not a tar stream, but long enough to be read as a header block"+strings.Repeat("x", 512)), io.Discard); err == nil {
		t.Error("garbage was accepted")
	}
}
