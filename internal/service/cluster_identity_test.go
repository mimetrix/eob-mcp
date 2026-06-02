package service

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/mimetrix/eob-mcp/internal/config"
	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

func TestImageTag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"image", ""},
		{"image:v1", "v1"},
		{"quay.io/foo/bar:rc6", "rc6"},
		{"172.31.44.247:5000/mantisnet/tawon-operator:rc6", "rc6"},
		{"quay.io/foo/bar@sha256:abc123", ""},
		{"quay.io/foo/bar:rc6@sha256:abc123", "rc6"},
	}
	for _, c := range cases {
		if got := imageTag(c.in); got != c.want {
			t.Errorf("imageTag(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestClusterIdentity_NoKube(t *testing.T) {
	srv := New(testIdentityConfig(), nil, nil, nil)
	resp, err := srv.ClusterIdentity(context.Background(), &eobv1.ClusterIdentityRequest{})
	if err != nil {
		t.Fatal(err)
	}
	// Identity from config is always echoed back; cluster-discovery fields
	// degrade to empty when kube is nil.
	if got := resp.GetCluster().GetSiteId(); got != "test-site" {
		t.Errorf("SiteId=%q, want test-site", got)
	}
	if resp.GetK8SVersion() != "" {
		t.Errorf("K8SVersion=%q, want empty", resp.GetK8SVersion())
	}
	if resp.GetEobVersion() != "" {
		t.Errorf("EobVersion=%q, want empty", resp.GetEobVersion())
	}
	if got := resp.GetMcpVersion(); got != "test-mcp-version" {
		t.Errorf("McpVersion=%q, want test-mcp-version", got)
	}
}

func TestClusterIdentity_KubeWired(t *testing.T) {
	tests := []struct {
		name       string
		dep        *appsv1.Deployment
		wantEoBVer string
	}{
		{
			name: "version label preferred over image tag",
			dep: operatorDeployment("v2.39.36-rc1",
				containerImage("manager", "quay.io/mantisnet/tawon-operator:rc4")),
			wantEoBVer: "v2.39.36-rc1",
		},
		{
			name:       "falls back to manager container image tag",
			dep:        operatorDeployment("", containerImage("manager", "quay.io/mantisnet/tawon-operator:rc6")),
			wantEoBVer: "rc6",
		},
		{
			name: "falls back to first container image tag when no manager",
			dep: operatorDeployment("",
				containerImage("kube-rbac-proxy", "gcr.io/foo/kube-rbac-proxy:v0.18.0"),
				containerImage("notmanager", "quay.io/mantisnet/tawon-operator:rc4")),
			wantEoBVer: "v0.18.0",
		},
		{
			name:       "no version label, no containers → empty",
			dep:        operatorDeployment(""),
			wantEoBVer: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cs := fake.NewSimpleClientset(tc.dep)
			srv := New(testIdentityConfig(), cs, nil, nil)
			resp, err := srv.ClusterIdentity(context.Background(), &eobv1.ClusterIdentityRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if got := resp.GetEobVersion(); got != tc.wantEoBVer {
				t.Errorf("EobVersion=%q, want %q", got, tc.wantEoBVer)
			}
			// fake.NewSimpleClientset reports a non-empty version string
			// from Discovery; we just assert it's present rather than
			// pinning to the fake's specific value.
			if resp.GetK8SVersion() == "" {
				t.Errorf("K8SVersion should be non-empty when kube is wired")
			}
		})
	}
}

func TestClusterIdentity_OperatorDeploymentMissing(t *testing.T) {
	// No Deployment seeded → operator lookup returns NotFound, which the
	// helper silently swallows. K8s version still comes through.
	cs := fake.NewSimpleClientset()
	srv := New(testIdentityConfig(), cs, nil, nil)
	resp, err := srv.ClusterIdentity(context.Background(), &eobv1.ClusterIdentityRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetEobVersion() != "" {
		t.Errorf("EobVersion=%q, want empty when operator deployment missing", resp.GetEobVersion())
	}
	if resp.GetK8SVersion() == "" {
		t.Errorf("K8SVersion should be non-empty even when operator missing")
	}
}

func testIdentityConfig() *config.Config {
	return &config.Config{
		SiteID:                 "test-site",
		Tenant:                 "test-tenant",
		Region:                 "test-region",
		MCPVersion:             "test-mcp-version",
		OperatorNamespace:      "operators",
		OperatorDeploymentName: "tawon-operator-controller-manager",
	}
}

func operatorDeployment(versionLabel string, containers ...corev1.Container) *appsv1.Deployment {
	labels := map[string]string{}
	if versionLabel != "" {
		labels[operatorVersionLabel] = versionLabel
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "operators",
			Name:      "tawon-operator-controller-manager",
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: containers},
			},
		},
	}
}

func containerImage(name, image string) corev1.Container {
	return corev1.Container{Name: name, Image: image}
}
