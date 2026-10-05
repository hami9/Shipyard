package monitor

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"time"
)

// handshakeTimeout bounds one certificate read.
const handshakeTimeout = 5 * time.Second

// ServedCert makes a TLS handshake with addr for hostname (SNI) and returns
// the leaf certificate presented: what clients get, not what is stored.
// The chain is not verified, since only its dates are read, and Caddy's
// internal CA is in no system pool. Nothing is sent after the handshake.
func ServedCert(ctx context.Context, addr, hostname string) (*x509.Certificate, error) {
	ctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	d := tls.Dialer{Config: &tls.Config{
		ServerName:         hostname,
		InsecureSkipVerify: true, // dates only; see above
		MinVersion:         tls.VersionTLS12,
	}}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("TLS handshake with %s for %s: %w", addr, hostname, err)
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, errors.New("no certificate presented")
	}
	return certs[0], nil
}
