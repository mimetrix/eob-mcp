// Package k8s holds the Kubernetes client wiring used by tools that need
// to read cluster state (deployments, daemonsets, server version, CRDs).
//
// The package exposes a small concrete client built from in-cluster
// config (the common deployment path) with KUBECONFIG as a fallback for
// local development. Tools accept kubernetes.Interface directly so tests
// can substitute a fake.NewSimpleClientset without touching this package.
package k8s

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Client wraps the typed Kubernetes clientset and the loaded REST config.
// Keep this small; per-resource helpers live alongside the tools that use
// them, not here.
type Client struct {
	// Clientset is the typed kubernetes.Interface (core, apps, etc.).
	Clientset kubernetes.Interface

	// Config is the underlying REST config, retained so callers can build
	// additional typed/dynamic clients (CRDs, discovery) without re-running
	// the in-cluster vs kubeconfig resolution.
	Config *rest.Config
}

// New constructs a Client. It prefers in-cluster configuration (the
// production path when running as a Pod with a projected ServiceAccount
// token). If that fails, it falls back to a kubeconfig file resolved via
// the KUBECONFIG env var or ~/.kube/config. Returns an error if neither
// source yields a usable configuration.
func New() (*Client, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8s: build clientset: %w", err)
	}
	return &Client{Clientset: cs, Config: cfg}, nil
}

// loadConfig resolves a REST config, preferring in-cluster mode.
func loadConfig() (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	} else if !errors.Is(err, rest.ErrNotInCluster) {
		return nil, fmt.Errorf("k8s: in-cluster config: %w", err)
	}

	path := kubeconfigPath()
	if path == "" {
		return nil, errors.New("k8s: not running in-cluster and no kubeconfig found (set KUBECONFIG or place a config at ~/.kube/config)")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		return nil, fmt.Errorf("k8s: load kubeconfig %q: %w", path, err)
	}
	return cfg, nil
}

func kubeconfigPath() string {
	if p := os.Getenv("KUBECONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	candidate := filepath.Join(home, ".kube", "config")
	if _, err := os.Stat(candidate); err != nil {
		return ""
	}
	return candidate
}
