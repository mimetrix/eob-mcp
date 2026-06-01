package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
	"github.com/mimetrix/eob-mcp/internal/config"
	"github.com/mimetrix/eob-mcp/internal/service"
)

// TestBuildTLSConfig_Validation covers the flag-combination guards. The
// happy path (both cert + key set) is exercised end-to-end below.
func TestBuildTLSConfig_Validation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		opts    tlsOpts
		wantErr bool
		wantNil bool
	}{
		{name: "all empty disables tls", opts: tlsOpts{}, wantNil: true},
		{name: "client-ca alone is invalid", opts: tlsOpts{clientCAPath: "/x"}, wantErr: true},
		{name: "cert without key", opts: tlsOpts{certPath: "/c"}, wantErr: true},
		{name: "key without cert", opts: tlsOpts{keyPath: "/k"}, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := buildTLSConfig(c.opts)
			if c.wantErr && err == nil {
				t.Fatalf("expected error, got nil (cfg=%v)", got)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.wantNil && got != nil {
				t.Fatalf("expected nil cfg, got %v", got)
			}
		})
	}
}

// TestGRPCListenerTLS proves the TLS hooks are wired end-to-end:
// buildTLSConfig loads PEM files, buildGRPCServer wraps the listener in
// credentials.NewTLS, and a TLS-only client can call ClusterIdentity.
// Server cert is generated in-test (self-signed ECDSA, 24h validity).
func TestGRPCListenerTLS(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	certPath, keyPath, pemCert := writeSelfSignedCert(t, dir)

	cfg := &config.Config{SiteID: "site-test-tls", Tenant: "tenant-test", Region: "us", MCPVersion: "test"}
	svc := service.New(cfg, nil, nil, nil)

	tlsConf, err := buildTLSConfig(tlsOpts{certPath: certPath, keyPath: keyPath})
	if err != nil {
		t.Fatalf("buildTLSConfig: %v", err)
	}
	if tlsConf == nil {
		t.Fatal("buildTLSConfig returned nil with both cert+key set")
	}

	grpcSrv := buildGRPCServer(svc, tlsConf)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { grpcSrv.Stop() })
	go func() { _ = grpcSrv.Serve(lis) }()

	// Client trusts the server cert via a CA pool built from the same
	// PEM that the server is presenting. Same behavior an aggregator
	// would use against an eob-mcp's published cert.
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemCert) {
		t.Fatal("client CA pool: AppendCertsFromPEM returned false")
	}
	clientCreds := credentials.NewTLS(&tls.Config{
		RootCAs:    pool,
		ServerName: "localhost",
		MinVersion: tls.VersionTLS12,
	})

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(clientCreds))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	resp, err := eobv1.NewEoBServiceClient(conn).ClusterIdentity(ctx, &eobv1.ClusterIdentityRequest{})
	if err != nil {
		t.Fatalf("ClusterIdentity rpc over TLS: %v", err)
	}
	if got := resp.GetCluster().GetSiteId(); got != "site-test-tls" {
		t.Errorf("site_id: got %q, want %q", got, "site-test-tls")
	}
}

// writeSelfSignedCert mints a single self-signed ECDSA cert + key,
// writes them to dir, and returns (certPath, keyPath, certPEM). Cert
// is valid for "localhost" + 127.0.0.1 so the client can verify
// ServerName="localhost" against it.
func writeSelfSignedCert(t *testing.T, dir string) (certPath, keyPath string, certPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "eob-mcp-test"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath, certPEM
}
