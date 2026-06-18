// Package main is the eob-mcp server entrypoint.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
	"k8s.io/client-go/kubernetes"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
	"github.com/mimetrix/eob-mcp/internal/config"
	"github.com/mimetrix/eob-mcp/internal/k8s"
	"github.com/mimetrix/eob-mcp/internal/mcp"
	"github.com/mimetrix/eob-mcp/internal/service"
	"github.com/mimetrix/eob-mcp/internal/streams"
	"github.com/mimetrix/eob-mcp/internal/tools"
)

// Version info is overridden at build time via -ldflags.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

const (
	defaultHTTPListenAddr = ":8443"
	defaultGRPCListenAddr = ":9443"
	readHeaderTimeout     = 5 * time.Second
	writeTimeout          = 30 * time.Second
	idleTimeout           = 120 * time.Second
	shutdownGracePeriod   = 15 * time.Second
	maxRequestBodyBytes   = 1 << 20 // 1 MiB
)

// tlsOpts collects TLS-related flag values. Both listeners share one
// cert/key pair; tlsClientCA enables mTLS by requiring + verifying
// client certificates against the given CA. Empty fields disable
// the corresponding behavior.
type tlsOpts struct {
	certPath     string
	keyPath      string
	clientCAPath string
}

func main() {
	httpListen := flag.String("listen", defaultHTTPListenAddr, "HTTP/MCP address to listen on (host:port)")
	grpcListen := flag.String("grpc-listen", defaultGRPCListenAddr, "gRPC address to listen on (host:port); empty disables")
	tlsCert := flag.String("tls-cert", "", "TLS cert path (PEM). Empty disables TLS on both listeners. Both -tls-cert and -tls-key must be set together.")
	tlsKey := flag.String("tls-key", "", "TLS key path (PEM).")
	tlsClientCA := flag.String("tls-client-ca", "", "PEM bundle of CAs trusted for client certs. When set, both listeners require + verify client certs (mTLS).")
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, error")
	flag.Parse()

	logger := newLogger(*logLevel)
	slog.SetDefault(logger)

	logger.Info("eob-mcp starting",
		"version", version,
		"commit", commit,
		"date", date,
		"http_listen", *httpListen,
		"grpc_listen", *grpcListen,
		"tls", *tlsCert != "",
		"mtls", *tlsClientCA != "",
	)

	opts := tlsOpts{certPath: *tlsCert, keyPath: *tlsKey, clientCAPath: *tlsClientCA}
	if err := run(*httpListen, *grpcListen, opts, logger); err != nil {
		logger.Error("server exited with error", "err", err)
		os.Exit(1)
	}
	logger.Info("eob-mcp stopped cleanly")
}

func run(httpListen, grpcListen string, tlsCfg tlsOpts, logger *slog.Logger) error {
	cfg := config.FromEnv(version)
	logger.Info("identity resolved",
		"site_id", cfg.SiteID,
		"tenant", cfg.Tenant,
		"region", cfg.Region,
	)

	// k8s wiring is best-effort at startup: a missing cluster (local dev,
	// container without a ServiceAccount) should not prevent the server
	// from starting. Tools degrade to empty/stub output instead.
	kubeClient, err := k8s.New()
	if err != nil {
		logger.Warn("kubernetes client unavailable; cluster-aware tools will return empty results",
			"err", err)
		kubeClient = nil
	} else {
		logger.Info("kubernetes client initialized",
			"operator_ns", cfg.OperatorNamespace,
			"tawon_ns", cfg.TawonNamespace,
		)
	}

	svc, backends, closeStreams, err := buildService(cfg, kubeClient, logger)
	if err != nil {
		return err
	}
	defer closeStreams()

	mcpServer, err := buildMCPServer(svc, backends.haveDyn, backends.haveStreams)
	if err != nil {
		return err
	}

	tlsConf, err := buildTLSConfig(tlsCfg)
	if err != nil {
		return err
	}

	httpSrv := buildHTTPServer(httpListen, mcpServer, tlsConf, logger)
	grpcSrv := buildGRPCServer(svc, tlsConf)

	// Both listeners write to the same error channel; whichever fails
	// first triggers a shutdown of the other. A clean Shutdown/Stop is
	// not an error and produces no entry on this channel.
	serverErrs := make(chan error, 2)

	go func() {
		logger.Info("HTTP/MCP listener starting", "addr", httpListen, "tls", tlsConf != nil)
		var lerr error
		if tlsConf != nil {
			// Cert is already attached via httpSrv.TLSConfig; the
			// arguments to ListenAndServeTLS are empty when that's set.
			lerr = httpSrv.ListenAndServeTLS("", "")
		} else {
			lerr = httpSrv.ListenAndServe()
		}
		if lerr != nil && !errors.Is(lerr, http.ErrServerClosed) {
			serverErrs <- fmt.Errorf("http: %w", lerr)
		}
	}()

	var grpcLis net.Listener
	if grpcListen != "" {
		lis, lerr := net.Listen("tcp", grpcListen)
		if lerr != nil {
			return fmt.Errorf("grpc listen %s: %w", grpcListen, lerr)
		}
		grpcLis = lis
		go func() {
			logger.Info("gRPC listener starting", "addr", grpcListen, "tls", tlsConf != nil)
			if serr := grpcSrv.Serve(grpcLis); serr != nil && !errors.Is(serr, grpc.ErrServerStopped) {
				serverErrs <- fmt.Errorf("grpc: %w", serr)
			}
		}()
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	select {
	case lerr := <-serverErrs:
		// One listener failed; tear down the other before returning.
		shutdownAll(httpSrv, grpcSrv, logger)
		return lerr
	case sig := <-sigs:
		logger.Info("shutdown signal received", "signal", sig.String())
	}

	shutdownAll(httpSrv, grpcSrv, logger)
	return nil
}

// serviceBackends summarizes which backends were wired into the service.
// Used by buildMCPServer to register only the tools whose backends are
// live — so `tools/list` doesn't advertise endpoints that will only
// return errors.
type serviceBackends struct {
	haveDyn     bool
	haveStreams bool
}

// buildService constructs the in-process gRPC service. Same instance is
// shared by the MCP wrappers (via tools.New*) and the gRPC listener (via
// eobv1.RegisterEoBServiceServer) — single source of truth.
//
// Backends are independently best-effort: a missing kube client puts
// the identity/health/resource_* RPCs into degraded mode; a missing
// streams reader puts Stream* RPCs into a clean error path. Neither
// failure prevents startup. The returned closer should be invoked on
// shutdown to drain the streams connection. The returned backends
// struct reports which surfaces are actually live so the MCP
// registration honors reality (not just config intent).
func buildService(cfg *config.Config, kube *k8s.Client, logger *slog.Logger) (*service.Server, serviceBackends, func(), error) {
	var b serviceBackends
	var kc kubernetes.Interface
	if kube != nil {
		kc = kube.Clientset
	}
	var dyn *k8s.DynClient
	if kube != nil {
		d, err := k8s.NewDynClient(kube)
		if err != nil {
			return nil, b, func() {}, fmt.Errorf("dynamic client: %w", err)
		}
		dyn = d
		b.haveDyn = true
		slog.Info("dynamic client built")
	}

	// NATS endpoint resolution: explicit env wins; otherwise discover the
	// chart-rendered streamstore Service via label so we don't bake the
	// chart's per-install hex suffix into the deploy manifest.
	if cfg.NATSURL == "" && kube != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		discovered, derr := kube.DiscoverStreamStoreURL(ctx, cfg.TawonNamespace)
		cancel()
		switch {
		case derr != nil:
			logger.Warn("streamstore Service discovery failed; Stream* RPCs disabled",
				"err", derr, "ns", cfg.TawonNamespace)
		case discovered == "":
			logger.Info("no streamstore Service found; Stream* RPCs disabled",
				"ns", cfg.TawonNamespace)
		default:
			cfg.NATSURL = discovered
			logger.Info("discovered streamstore Service", "url", cfg.NATSURL)
		}
	}

	var streamsReader streams.Reader
	closer := func() {}
	if cfg.NATSURL != "" {
		r, err := streams.DialJetStream(cfg.NATSURL)
		if err != nil {
			logger.Warn("streams backend unavailable; Stream* RPCs disabled",
				"err", err, "url", cfg.NATSURL)
		} else {
			streamsReader = r
			closer = func() { _ = r.Close() }
			b.haveStreams = true
			logger.Info("streams backend connected", "url", cfg.NATSURL)
		}
	} else if kube == nil {
		logger.Info("EOB_NATS_URL unset and no kube client for discovery; Stream* RPCs disabled")
	}

	return service.New(cfg, kc, dyn, streamsReader), b, closer, nil
}

// buildMCPServer registers each MCP tool against the shared service.
// Tools that depend on backends not currently available (dyn client,
// streams reader) are skipped entirely rather than registered with a
// guaranteed-error path — keeps tools/list output honest about what
// the server can actually do right now.
func buildMCPServer(svc *service.Server, haveDyn, haveStreams bool) (*mcp.Server, error) {
	s := mcp.NewServer("eob-mcp", version)
	for _, t := range []mcp.ToolHandler{
		tools.NewClusterIdentity(svc),
		tools.NewEoBHealth(svc),
		tools.NewTraceHealth(svc),
	} {
		if err := s.RegisterTool(t); err != nil {
			return nil, err
		}
	}
	slog.Info("registering resource tools", "kube_present", haveDyn)
	if haveDyn {
		for _, t := range []mcp.ToolHandler{
			tools.NewResourceList(svc),
			tools.NewResourceGet(svc),
			tools.NewResourceApply(svc),
			tools.NewResourceDelete(svc),
			tools.NewResourceSchema(svc),
		} {
			if err := s.RegisterTool(t); err != nil {
				return nil, err
			}
			slog.Info("registered tool", "name", t.Name())
		}
	}
	slog.Info("registering stream tools", "streams_present", haveStreams)
	if haveStreams {
		for _, t := range []mcp.ToolHandler{
			tools.NewStreamList(svc),
			tools.NewStreamStats(svc),
			tools.NewStreamRead(svc),
		} {
			if err := s.RegisterTool(t); err != nil {
				return nil, err
			}
			slog.Info("registered tool", "name", t.Name())
		}
	}
	return s, nil
}

func buildHTTPServer(addr string, mcpServer *mcp.Server, tlsConf *tls.Config, logger *slog.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthzHandler)
	mux.HandleFunc("/readyz", readyzHandler)
	mux.HandleFunc("/version", versionHandler)
	mux.Handle("/mcp", mcpServer)
	return &http.Server{
		Addr:              addr,
		Handler:           withRequestLimits(mux),
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		TLSConfig:         tlsConf,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
}

// buildGRPCServer constructs the gRPC server with EoBService registered.
// Reflection is enabled so grpcurl can introspect the service surface
// without needing the .proto files on the client side. A non-nil
// tlsConf wraps the server in TLS credentials (same cert/key as HTTP);
// nil leaves it on insecure credentials for plaintext dev mode.
func buildGRPCServer(svc *service.Server, tlsConf *tls.Config) *grpc.Server {
	var opts []grpc.ServerOption
	if tlsConf != nil {
		opts = append(opts, grpc.Creds(credentials.NewTLS(tlsConf)))
	} else {
		opts = append(opts, grpc.Creds(insecure.NewCredentials()))
	}
	s := grpc.NewServer(opts...)
	eobv1.RegisterEoBServiceServer(s, svc)
	reflection.Register(s)
	return s
}

// buildTLSConfig loads the configured cert/key and (optionally) the
// client-CA bundle. Returns nil when no cert is configured, leaving
// both listeners on plaintext. Validation: both -tls-cert and -tls-key
// must be set together; -tls-client-ca alone is rejected.
func buildTLSConfig(opts tlsOpts) (*tls.Config, error) {
	if opts.certPath == "" && opts.keyPath == "" {
		if opts.clientCAPath != "" {
			return nil, fmt.Errorf("tls: -tls-client-ca requires -tls-cert and -tls-key")
		}
		return nil, nil
	}
	if opts.certPath == "" || opts.keyPath == "" {
		return nil, fmt.Errorf("tls: -tls-cert and -tls-key must be set together")
	}
	cert, err := tls.LoadX509KeyPair(opts.certPath, opts.keyPath)
	if err != nil {
		return nil, fmt.Errorf("tls: load keypair: %w", err)
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	if opts.clientCAPath != "" {
		caPEM, err := os.ReadFile(opts.clientCAPath)
		if err != nil {
			return nil, fmt.Errorf("tls: read client CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("tls: client CA bundle %q contains no usable certs", opts.clientCAPath)
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg, nil
}

// shutdownAll drains both listeners with a single shared deadline. HTTP
// uses Shutdown (in-flight requests get to finish); gRPC uses
// GracefulStop with a fallback to Stop if the grace period expires.
func shutdownAll(httpSrv *http.Server, grpcSrv *grpc.Server, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownGracePeriod)
	defer cancel()

	if err := httpSrv.Shutdown(ctx); err != nil {
		logger.Error("http shutdown", "err", err)
	}

	done := make(chan struct{})
	go func() {
		grpcSrv.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		logger.Warn("grpc graceful stop exceeded grace period; forcing stop")
		grpcSrv.Stop()
	}
}

// withRequestLimits applies safety caps to every request before it reaches handlers.
func withRequestLimits(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("handler panic recovered",
					"path", r.URL.Path,
					"panic", rec,
					"stack", string(debug.Stack()),
				)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func readyzHandler(w http.ResponseWriter, _ *http.Request) {
	// In future commits this will check NATS + k8s connectivity. For now,
	// readiness == liveness.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready\n"))
}

func versionHandler(w http.ResponseWriter, _ *http.Request) {
	resp := struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Date    string `json:"date"`
	}{version, commit, date}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}
