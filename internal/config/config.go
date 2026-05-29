// Package config holds runtime configuration loaded from environment
// variables. Kept tiny and explicit; no auto-discovery, no file parsing.
package config

import "os"

// Config is the in-process runtime configuration. All fields are
// optional in this phase; cluster_identity will report empty strings
// for any field not set. Later phases may add validation.
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
}

// FromEnv loads config from environment variables. Empty values are
// preserved; consumers should treat missing fields as "unknown" rather
// than as errors.
//
// Environment variables read:
//   EOB_SITE_ID
//   EOB_TENANT
//   EOB_REGION
func FromEnv(mcpVersion string) *Config {
	return &Config{
		SiteID:     os.Getenv("EOB_SITE_ID"),
		Tenant:     os.Getenv("EOB_TENANT"),
		Region:     os.Getenv("EOB_REGION"),
		MCPVersion: mcpVersion,
	}
}
