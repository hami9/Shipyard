package api

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// Listen opens addr, which is either host:port or unix:/absolute/path.sock.
// A Unix socket is created with mode 0660 so only the owner and group (for
// example Caddy's) can connect. A stale socket from a previous run is removed;
// any other file at that path is left alone and reported as an error.
func Listen(addr string) (net.Listener, error) {
	path, ok := strings.CutPrefix(addr, "unix:")
	if !ok {
		return net.Listen("tcp", addr)
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode().Type() != fs.ModeSocket {
			return nil, fmt.Errorf("listen %s: path exists and is not a socket", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale socket %s: %w", path, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o660); err != nil {
		ln.Close()
		return nil, fmt.Errorf("chmod %s: %w", path, err)
	}
	return ln, nil
}

// Serve runs an HTTP server on ln until ctx is cancelled, then shuts down
// gracefully, giving in-flight requests up to shutdownTimeout to finish.
func Serve(ctx context.Context, ln net.Listener, h http.Handler, shutdownTimeout time.Duration, log *slog.Logger) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	log.Info("api listening", slog.String("addr", ln.Addr().String()))

	select {
	case err := <-errc:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	log.Info("api shutting down", slog.Duration("timeout", shutdownTimeout))
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
