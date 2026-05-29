package tools

import (
	"encoding/json"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes/fake"
	fakediscovery "k8s.io/client-go/discovery/fake"

	"github.com/mimetrix/eob-mcp/internal/config"
)

func newTestConfig() *config.Config {
	return &config.Config{
		SiteID:                 "site-x",
		Tenant:                 "tenant-y",
		Region:                 "us-east-2",
		MCPVersion:             "test",
		OperatorNamespace:      "operators",
		TawonNamespace:         "tawon-operator",
		OperatorDeploymentName: "tawon-operator-controller-manager",
		WebhookConfigName:      "eob-mutate",
		DirectiveLabelSelector: "app.kubernetes.io/name=tawon-directive",
	}
}

// fakeWithServerVersion returns a fake clientset whose Discovery()
// reports the given GitVersion.
func fakeWithServerVersion(t *testing.T, gitVersion string) *fake.Clientset {
	t.Helper()
	cs := fake.NewSimpleClientset()
	d, ok := cs.Discovery().(*fakediscovery.FakeDiscovery)
	if !ok {
		t.Fatalf("expected *fakediscovery.FakeDiscovery, got %T", cs.Discovery())
	}
	d.FakedServerVersion = &version.Info{GitVersion: gitVersion}
	return cs
}

func TestClusterIdentity_NoKubeReturnsEmptyVersions(t *testing.T) {
	t.Parallel()
	tool := NewClusterIdentity(newTestConfig(), nil)
	res, err := tool.Call(t.Context(), nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	got := parseIdentity(t, res.Content[0].Text)
	if got["site_id"] != "site-x" {
		t.Errorf("site_id: got %q, want %q", got["site_id"], "site-x")
	}
	if got["k8s_version"] != "" {
		t.Errorf("k8s_version: got %q, want empty", got["k8s_version"])
	}
	if got["eob_version"] != "" {
		t.Errorf("eob_version: got %q, want empty", got["eob_version"])
	}
}

func TestClusterIdentity_ReportsServerVersion(t *testing.T) {
	t.Parallel()
	cs := fakeWithServerVersion(t, "v1.31.4")
	tool := NewClusterIdentity(newTestConfig(), cs)
	res, err := tool.Call(t.Context(), nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	got := parseIdentity(t, res.Content[0].Text)
	if got["k8s_version"] != "v1.31.4" {
		t.Errorf("k8s_version: got %q, want %q", got["k8s_version"], "v1.31.4")
	}
}

func TestClusterIdentity_ReportsEoBVersionFromOperatorDeployment(t *testing.T) {
	t.Parallel()
	cfg := newTestConfig()
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: cfg.OperatorDeploymentName, Namespace: cfg.OperatorNamespace},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "manager", Image: "quay.io/mantisnet/tawon-operator:rc6"},
					},
				},
			},
		},
	}
	cs := fake.NewSimpleClientset(dep)
	tool := NewClusterIdentity(cfg, cs)
	res, err := tool.Call(t.Context(), nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	got := parseIdentity(t, res.Content[0].Text)
	if got["eob_version"] != "rc6" {
		t.Errorf("eob_version: got %q, want %q", got["eob_version"], "rc6")
	}
}

// When both the app.kubernetes.io/version label and a parseable image
// tag are present, the label wins because it carries the Helm chart's
// appVersion — stable across image re-tags.
func TestClusterIdentity_PrefersVersionLabelOverImageTag(t *testing.T) {
	t.Parallel()
	cfg := newTestConfig()
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.OperatorDeploymentName,
			Namespace: cfg.OperatorNamespace,
			Labels:    map[string]string{"app.kubernetes.io/version": "v2.39.36-rc1"},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "manager", Image: "quay.io/mantisnet/tawon-operator:dev"},
					},
				},
			},
		},
	}
	cs := fake.NewSimpleClientset(dep)
	tool := NewClusterIdentity(cfg, cs)
	res, err := tool.Call(t.Context(), nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	got := parseIdentity(t, res.Content[0].Text)
	if got["eob_version"] != "v2.39.36-rc1" {
		t.Errorf("eob_version: got %q, want %q", got["eob_version"], "v2.39.36-rc1")
	}
}

// With multiple containers, eob_version should prefer the conventionally
// named "manager" container over the first one (which on a kubebuilder
// operator is often kube-rbac-proxy).
func TestClusterIdentity_PrefersManagerContainerImage(t *testing.T) {
	t.Parallel()
	cfg := newTestConfig()
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: cfg.OperatorDeploymentName, Namespace: cfg.OperatorNamespace},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "kube-rbac-proxy", Image: "gcr.io/kubebuilder/kube-rbac-proxy:v0.16.0"},
						{Name: "manager", Image: "quay.io/mantisnet/tawon-operator:rc7"},
					},
				},
			},
		},
	}
	cs := fake.NewSimpleClientset(dep)
	tool := NewClusterIdentity(cfg, cs)
	res, err := tool.Call(t.Context(), nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	got := parseIdentity(t, res.Content[0].Text)
	if got["eob_version"] != "rc7" {
		t.Errorf("eob_version: got %q, want %q (manager-container tag)", got["eob_version"], "rc7")
	}
}

func TestClusterIdentity_MissingOperatorDeploymentLeavesEoBVersionEmpty(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset() // no objects
	tool := NewClusterIdentity(newTestConfig(), cs)
	res, err := tool.Call(t.Context(), nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	got := parseIdentity(t, res.Content[0].Text)
	if got["eob_version"] != "" {
		t.Errorf("eob_version: got %q, want empty (operator deployment absent)", got["eob_version"])
	}
}

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

func parseIdentity(t *testing.T, body string) map[string]string {
	t.Helper()
	var got map[string]string
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal identity: %v\nbody=%s", err, body)
	}
	return got
}
