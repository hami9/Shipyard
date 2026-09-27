package build

import (
	"fmt"
	"strings"
	"testing"
)

func TestBoundedLog(t *testing.T) {
	var got []string
	l := newBoundedLog(100, func(s string) { got = append(got, s) })
	for i := range 50 {
		fmt.Fprintf(l, "line %02d of the build output\n", i) // 28 bytes per line
	}
	l.Close()
	// 3 lines fit in 100 bytes (3*28 = 84, a fourth would be 112), then one notice.
	if len(got) != 4 || got[0] != "line 00 of the build output" || !strings.Contains(got[3], "truncated at 100 bytes") {
		t.Fatalf("forwarded %d lines: %q", len(got), got)
	}
	tail := strings.Split(l.Tail(), "\n")
	if len(tail) != tailLines || tail[len(tail)-1] != "line 49 of the build output" {
		t.Fatalf("tail = %q", tail)
	}
}

func TestBoundedLogPartialWritesAndHugeLine(t *testing.T) {
	var got []string
	l := newBoundedLog(1<<20, func(s string) { got = append(got, s) })
	l.Write([]byte("#5 [2/3] RU"))
	l.Write([]byte("N make\n#5 DONE 0.1s\n"))
	l.Write([]byte(strings.Repeat("x", 2<<20) + "\n")) // over the scanner's max line
	l.Write([]byte("after\n"))
	l.Close() // must not hang: the rest is drained
	if len(got) < 2 || got[0] != "#5 [2/3] RUN make" || got[1] != "#5 DONE 0.1s" {
		t.Fatalf("lines = %q", got)
	}
}

func TestValidate(t *testing.T) {
	ok := Request{Dockerfile: "/w/op/Dockerfile", Context: "/w/op", App: "web",
		Commit: strings.Repeat("a", 40), DeploymentID: "11111111-1111-4111-8111-111111111111"}
	if err := validate(ok); err != nil {
		t.Fatalf("valid request: %v", err)
	}
	for name, edit := range map[string]func(*Request){
		"relative dockerfile": func(r *Request) { r.Dockerfile = "Dockerfile" },
		"relative context":    func(r *Request) { r.Context = "." },
		"bad slug":            func(r *Request) { r.App = "--push" },
		"short commit":        func(r *Request) { r.Commit = "abc" },
		"bad deployment":      func(r *Request) { r.DeploymentID = "x" },
	} {
		r := ok
		edit(&r)
		if validate(r) == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestCleanEnvDropsShipyardVariables(t *testing.T) {
	t.Setenv("SHIPYARD_DATABASE_URL", "postgres://u:secret@db/x")
	t.Setenv("SHIPYARD_KEK_ACTIVE", "k1")
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	env := strings.Join(cleanEnv(), "\n")
	if strings.Contains(env, "SHIPYARD_") || !strings.Contains(env, "DOCKER_HOST=unix:///var/run/docker.sock") {
		t.Fatalf("cleanEnv = %q", env)
	}
}
