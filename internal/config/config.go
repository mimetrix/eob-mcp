// Package config holds runtime configuration loaded from environment
// variables. Kept tiny and explicit; no auto-discovery, no file parsing.
package config

import "os"

const (
	defaultOperatorNamespace = "operators"
	defaultTawonNamespace    = "tawon-operator"
)

// Config is the in-process runtime configuration. All identity fields are
// optional; cluster_identity reports empty strings for any unset value.
// Namespace fields always carry a default so k8s lookups have a target.
type Config struct {
	// SiteID identifies the cluster this server is running in.
	// On F5 XC, this is the site name like "srikan-tf-test-0".
	SiteID string

	// Tenant is the platform tenant ID, e.g. "platform-svc-nbryikfr".
	Tenant string

	// Region is the deployment region, e.g. "us-east-2".
	Region string

	// MCPVersion is the build-time version of this server. Set by the
	// caller (typically from main's version variable).
	MCPVersion string

	// OperatorNamespace is where the tawon-operator Deployment lives.
	// Default: "operators".
	OperatorNamespace string

	// TawonNamespace is where the EoB workload pods (dashboard,
	// streamstore, webhook, agent DS) and eob-mcp itself run.
	// Default: "tawon-operator".
	TawonNamespace string
}

// FromEnv loads config from environment variables. Identity fields default
// to empty string; namespace fields default to the EoB conventions.
//
// Environment variables read:
//
//	EOB_SITE_ID
//	EOB_TENANT
//	EOB_REGION
//	EOB_OPERATOR_NAMESPACE  (default: "operators")
//	EOB_TAWON_NAMESPACE     (default: "tawon-operator")
func FromEnv(mcpVersion string) *Config {
	return &Config{
		SiteID:            os.Getenv("EOB_SITE_ID"),
		Tenant:            os.Getenv("EOB_TENANT"),
		Region:            os.Getenv("EOB_REGION"),
		MCPVersion:        mcpVersion,
		OperatorNamespace: envOrDefault("EOB_OPERATOR_NAMESPACE", defaultOperatorNamespace),
		TawonNamespace:    envOrDefault("EOB_TAWON_NAMESPACE", defaultTawonNamespace),
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
