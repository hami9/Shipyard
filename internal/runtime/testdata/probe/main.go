// Command probe runs inside the docker-tagged runtime tests. It shows the test
// what the kernel enforces from inside the container: process status (caps,
// no_new_privs), cgroup limits, and the injected environment.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	http.HandleFunc("GET /read", func(w http.ResponseWriter, r *http.Request) {
		b, err := os.ReadFile(r.URL.Query().Get("path"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Write(b)
	})
	http.HandleFunc("GET /env", func(w http.ResponseWriter, r *http.Request) {
		v, ok := os.LookupEnv(r.URL.Query().Get("key"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(v))
	})
	// Prints a line to stdout, or with stream=stderr to stderr, for the log tests.
	http.HandleFunc("GET /say", func(w http.ResponseWriter, r *http.Request) {
		out := os.Stdout
		if r.URL.Query().Get("stream") == "stderr" {
			out = os.Stderr
		}
		fmt.Fprintln(out, r.URL.Query().Get("text"))
	})
	// Optional failure modes for the worker's docker tests.
	if os.Getenv("PROBE_EXIT") != "" {
		log.Fatal("probe exiting on request")
	}
	status := http.StatusOK
	if os.Getenv("PROBE_UNHEALTHY") != "" {
		status = http.StatusInternalServerError
	}
	http.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) })
	log.Print("probe listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
