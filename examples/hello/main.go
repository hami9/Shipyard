// Command hello is the sample app for Shipyard's acceptance demo
// (docs/ACCEPTANCE.md). It answers on / with its version and greeting, and
// on /healthz with its health, which the demo's "broken change" turns off.
package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// version is what the demo's "working change" edits.
const version = "v1"

// healthy is what the demo's "broken change" sets to false: the build
// succeeds, the health check fails, and the previous release keeps serving.
const healthy = true

func main() {
	greeting := cmp.Or(os.Getenv("GREETING"), "hello")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s from %s\n", greeting, version)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if !healthy {
			http.Error(w, "unhealthy", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ok")
	})
	srv := &http.Server{
		Addr:              ":" + cmp.Or(os.Getenv("PORT"), "8080"),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Shipyard stops a superseded release with SIGTERM: finish requests in
	// flight, then exit.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	log.Printf("hello %s listening on %s", version, srv.Addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
