// Command probe runs inside the docker-tagged runtime tests. It shows the test
// what the kernel enforces from inside the container: process status (caps,
// no_new_privs), cgroup limits, and the injected environment.
package main

import (
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
	log.Fatal(http.ListenAndServe(":8080", nil))
}
