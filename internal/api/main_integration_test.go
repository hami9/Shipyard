//go:build integration

package api

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

// TestMain fails a full integration run (no -run filter) that leaves a
// success response in api/openapi.json unprovoked: conform has then checked
// every one of them against a real handler at least once.
func TestMain(m *testing.M) {
	code := m.Run()
	if f := flag.Lookup("test.run"); code == 0 && f != nil && f.Value.String() == "" {
		if missing := unseenSuccesses(); len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "FAIL: %d success responses in api/openapi.json were never provoked by a test:\n  %s\n",
				len(missing), strings.Join(missing, "\n  "))
			code = 1
		}
	}
	os.Exit(code)
}

// unseenSuccesses lists the spec's success responses no test provoked.
func unseenSuccesses() []string {
	s, err := loadSpec()
	if err != nil {
		return []string{err.Error()}
	}
	var out []string
	for pattern, op := range s.ops {
		for status := range op.Responses {
			if strings.HasPrefix(status, "2") {
				if _, ok := seen.Load(pattern + " " + status); !ok {
					out = append(out, pattern+" "+status)
				}
			}
		}
	}
	slices.Sort(out)
	return out
}
