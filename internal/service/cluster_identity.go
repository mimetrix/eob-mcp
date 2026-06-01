package service

import (
	"context"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

// k8sCallTimeout bounds every API call ClusterIdentity makes so a slow
// or unreachable apiserver cannot stall request processing.
const k8sCallTimeout = 5 * time.Second

// operatorVersionLabel is the standard Helm/Kubebuilder label carrying
// the chart's appVersion. When present on the operator Deployment, it is
// preferred over the container image tag because it tracks the released
// version rather than whatever tag the in-cluster registry happens to
// ship under.
const operatorVersionLabel = "app.kubernetes.io/version"

// operatorManagerContainer is the conventional name of the controller
// container in a kubebuilder-generated operator. eob_version falls back
// to this container's image tag when no version label is set.
const operatorManagerContainer = "manager"

// ClusterIdentity returns the cluster identity block used by fleet
// consumers to label results coming back from this server.
func (s *Server) ClusterIdentity(ctx context.Context, _ *eobv1.ClusterIdentityRequest) (*eobv1.ClusterIdentityResponse, error) {
	return &eobv1.ClusterIdentityResponse{
		Cluster: &eobv1.ClusterRef{
			SiteId: s.cfg.SiteID,
			Tenant: s.cfg.Tenant,
			Region: s.cfg.Region,
		},
		K8SVersion: s.k8sVersion(ctx),
		EobVersion: s.eobVersion(ctx),
		McpVersion: s.cfg.MCPVersion,
	}, nil
}

// k8sVersion returns the server's reported GitVersion (e.g. "v1.31.4"),
// or "" if no kube client is wired or the call fails. Failures are
// silenced because identity is best-effort metadata.
func (s *Server) k8sVersion(_ context.Context) string {
	if s.kube == nil {
		return ""
	}
	info, err := s.kube.Discovery().ServerVersion()
	if err != nil {
		return ""
	}
	return info.GitVersion
}

// eobVersion derives the EoB platform version from the operator
// Deployment. Prefers the Helm-stamped app.kubernetes.io/version label
// (most stable across re-tagged images), falling back to the "manager"
// container's image tag, then the first container's image tag.
func (s *Server) eobVersion(ctx context.Context) string {
	if s.kube == nil {
		return ""
	}
	callCtx, cancel := context.WithTimeout(ctx, k8sCallTimeout)
	defer cancel()
	dep, err := s.kube.AppsV1().
		Deployments(s.cfg.OperatorNamespace).
		Get(callCtx, s.cfg.OperatorDeploymentName, metav1.GetOptions{})
	if err != nil {
		return ""
	}
	if v := dep.Labels[operatorVersionLabel]; v != "" {
		return v
	}
	containers := dep.Spec.Template.Spec.Containers
	for i := range containers {
		if containers[i].Name == operatorManagerContainer {
			if tag := imageTag(containers[i].Image); tag != "" {
				return tag
			}
			break
		}
	}
	if len(containers) == 0 {
		return ""
	}
	return imageTag(containers[0].Image)
}

// imageTag returns the tag portion of an OCI image reference, or "" if
// no tag is present. Handles registries with a port (host:port/path:tag)
// by splitting on the rightmost colon after the last slash. A digest
// suffix is stripped first so "repo:tag@sha256:..." still yields "tag".
func imageTag(ref string) string {
	if ref == "" {
		return ""
	}
	if at := strings.Index(ref, "@"); at >= 0 {
		ref = ref[:at]
	}
	slash := strings.LastIndex(ref, "/")
	tail := ref
	if slash >= 0 {
		tail = ref[slash+1:]
	}
	colon := strings.LastIndex(tail, ":")
	if colon < 0 {
		return ""
	}
	return tail[colon+1:]
}
