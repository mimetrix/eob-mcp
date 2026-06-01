// Package config holds runtime configuration loaded from environment
// variables. Kept tiny and explicit; no auto-discovery, no file parsing.
package config

import "os"

const (
	defaultOperatorNamespace      = "operators"
	defaultTawonNamespace         = "tawon-operator"
	defaultOperatorDeploymentName = "tawon-operator-controller-manager"
	defaultWebhookConfigName      = "eob-mutate"
	defaultDirectiveSelector      = "app.kubernetes.io/name=tawon-directive"
	defaultCRDAPIGroup            = "tawon.mantisnet.com"
	defaultFieldManager           = "eob-mcp"
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

	// OperatorNamespace is where the tawon-operator controller-manager
	// Deployment lives. Default: "operators".
	OperatorNamespace string

	// TawonNamespace is where the EoB workload pods (dashboard,
	// streamstore, per-directive DaemonSets) and eob-mcp itself run.
	// Default: "tawon-operator".
	TawonNamespace string

	// OperatorDeploymentName is the name of the operator Deployment
	// within OperatorNamespace. Default matches the rc4+ chart's
	// kubebuilder-style name; override if a downstream chart renames it.
	OperatorDeploymentName string

	// WebhookConfigName is the cluster-scoped MutatingWebhookConfiguration
	// installed by the EoB stack for per-directive port remapping.
	// Default: "eob-mutate".
	WebhookConfigName string

	// DirectiveLabelSelector matches every DaemonSet (and pod) that the
	// operator spawns for a ClusterDirective. The default keys off the
	// canonical app.kubernetes.io/name label the operator stamps on its
	// agent pods. Format: standard Kubernetes label selector.
	DirectiveLabelSelector string

	// CRDAPIGroup is the default Kubernetes API group used by the generic
	// resource_* tools when the caller doesn't specify one. Default:
	// "tawon.mantisnet.com" so EoB CRDs are addressable by Kind alone.
	// Callers can always override per-call.
	CRDAPIGroup string

	// FieldManager is the field-manager identity used in Server-Side Apply
	// operations (resource_apply). Conflicts with other managers will be
	// reported with this name. Default: "eob-mcp".
	FieldManager string

	// NATSURL is the JetStream endpoint used by the Stream* RPCs. Empty
	// disables the data plane (Stream* RPCs return a configuration
	// error). Typical in-cluster value:
	//
	//   nats://tawon-streamstore.tawon-operator.svc.cluster.local:4222
	NATSURL string
}

// FromEnv loads config from environment variables. Identity fields default
// to empty string; namespace and resource-name fields default to the EoB
// conventions and are only worth overriding when a downstream chart
// renames things.
//
// Environment variables read:
//
//	EOB_SITE_ID
//	EOB_TENANT
//	EOB_REGION
//	EOB_OPERATOR_NAMESPACE   (default: "operators")
//	EOB_TAWON_NAMESPACE      (default: "tawon-operator")
//	EOB_OPERATOR_DEPLOYMENT  (default: "tawon-operator-controller-manager")
//	EOB_WEBHOOK_CONFIG       (default: "eob-mutate")
//	EOB_DIRECTIVE_SELECTOR   (default: "app.kubernetes.io/name=tawon-directive")
//	EOB_CRD_API_GROUP        (default: "tawon.mantisnet.com")
//	EOB_FIELD_MANAGER        (default: "eob-mcp")
//	EOB_NATS_URL             (default: empty; Stream* RPCs disabled if unset)
func FromEnv(mcpVersion string) *Config {
	return &Config{
		SiteID:                 os.Getenv("EOB_SITE_ID"),
		Tenant:                 os.Getenv("EOB_TENANT"),
		Region:                 os.Getenv("EOB_REGION"),
		MCPVersion:             mcpVersion,
		OperatorNamespace:      envOrDefault("EOB_OPERATOR_NAMESPACE", defaultOperatorNamespace),
		TawonNamespace:         envOrDefault("EOB_TAWON_NAMESPACE", defaultTawonNamespace),
		OperatorDeploymentName: envOrDefault("EOB_OPERATOR_DEPLOYMENT", defaultOperatorDeploymentName),
		WebhookConfigName:      envOrDefault("EOB_WEBHOOK_CONFIG", defaultWebhookConfigName),
		DirectiveLabelSelector: envOrDefault("EOB_DIRECTIVE_SELECTOR", defaultDirectiveSelector),
		CRDAPIGroup:            envOrDefault("EOB_CRD_API_GROUP", defaultCRDAPIGroup),
		FieldManager:           envOrDefault("EOB_FIELD_MANAGER", defaultFieldManager),
		NATSURL:                os.Getenv("EOB_NATS_URL"),
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
