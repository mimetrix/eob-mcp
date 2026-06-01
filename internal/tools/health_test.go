package tools

import (
	"encoding/json"
	"testing"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/mimetrix/eob-mcp/internal/config"
	"github.com/mimetrix/eob-mcp/internal/service"
)

func newHealthTool(cfg *config.Config, kube kubernetes.Interface) *EoBHealth {
	return NewEoBHealth(service.New(cfg, kube, nil, nil))
}

func TestEoBHealth_NoKubeReturnsStub(t *testing.T) {
	t.Parallel()
	tool := newHealthTool(newTestConfig(), nil)
	res, err := tool.Call(t.Context(), nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	got := parseHealth(t, res.Content[0].Text)
	if got["cluster_state"] != "no-cluster" {
		t.Errorf("cluster_state: got %v, want %q", got["cluster_state"], "no-cluster")
	}
}

func TestEoBHealth_AllComponentsAbsentWhenEmptyCluster(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset()
	tool := newHealthTool(newTestConfig(), cs)
	res, err := tool.Call(t.Context(), nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	got := parseHealth(t, res.Content[0].Text)
	components := mustMap(t, got["components"])
	for _, key := range []string{"operator", "dashboard", "streamstore", "webhook", "agent"} {
		comp := mustMap(t, components[key])
		if comp["status"] != "absent" {
			t.Errorf("%s: status=%v, want absent", key, comp["status"])
		}
	}
	directives, _ := got["directives"].([]any)
	if len(directives) != 0 {
		t.Errorf("directives: got %d entries, want 0", len(directives))
	}
}

func TestEoBHealth_HealthyStackReportsOK(t *testing.T) {
	t.Parallel()
	cfg := newTestConfig()
	one := int32(1)
	two := int32(2)
	cs := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cfg.OperatorDeploymentName, Namespace: cfg.OperatorNamespace},
			Spec:       appsv1.DeploymentSpec{Replicas: &one},
			Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "tawon-dashboard", Namespace: cfg.TawonNamespace},
			Spec:       appsv1.DeploymentSpec{Replicas: &two},
			Status:     appsv1.DeploymentStatus{ReadyReplicas: 2},
		},
		&appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: "tawon-streamstore", Namespace: cfg.TawonNamespace},
			Spec:       appsv1.StatefulSetSpec{Replicas: &two},
			Status:     appsv1.StatefulSetStatus{ReadyReplicas: 2},
		},
		&admissionv1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: cfg.WebhookConfigName},
			Webhooks:   []admissionv1.MutatingWebhook{{Name: "eob-mutate.f5.local"}},
		},
		newDirectiveDS(cfg.TawonNamespace, "tawon-directive-foo", 3, 3),
		newDirectiveDS(cfg.TawonNamespace, "tawon-directive-bar", 3, 3),
	)
	tool := newHealthTool(cfg, cs)
	res, err := tool.Call(t.Context(), nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	got := parseHealth(t, res.Content[0].Text)
	components := mustMap(t, got["components"])
	for _, key := range []string{"operator", "dashboard", "streamstore", "webhook", "agent"} {
		comp := mustMap(t, components[key])
		if comp["status"] != "ok" {
			t.Errorf("%s: status=%v, want ok", key, comp["status"])
		}
	}
	agent := mustMap(t, components["agent"])
	if agent["ready"].(float64) != 6 || agent["desired"].(float64) != 6 {
		t.Errorf("agent aggregate: got ready=%v desired=%v, want 6/6", agent["ready"], agent["desired"])
	}
	directives := got["directives"].([]any)
	if len(directives) != 2 {
		t.Fatalf("directives: got %d, want 2", len(directives))
	}
	// Sorted by name: bar before foo.
	if directives[0].(map[string]any)["name"] != "tawon-directive-bar" {
		t.Errorf("directives[0].name: got %v, want tawon-directive-bar (sorted)", directives[0].(map[string]any)["name"])
	}
}

func TestEoBHealth_DegradedDirectiveDaemonSet(t *testing.T) {
	t.Parallel()
	cfg := newTestConfig()
	cs := fake.NewSimpleClientset(
		newDirectiveDS(cfg.TawonNamespace, "tawon-directive-foo", 3, 2),
	)
	tool := newHealthTool(cfg, cs)
	res, err := tool.Call(t.Context(), nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	got := parseHealth(t, res.Content[0].Text)
	components := mustMap(t, got["components"])
	agent := mustMap(t, components["agent"])
	if agent["status"] != "degraded" {
		t.Errorf("agent status: got %v, want degraded", agent["status"])
	}
	if agent["ready"].(float64) != 2 || agent["desired"].(float64) != 3 {
		t.Errorf("agent aggregate: got ready=%v desired=%v, want 2/3", agent["ready"], agent["desired"])
	}
}

func TestEoBHealth_WebhookAbsentWhenMWCMissing(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset() // no MWC
	tool := newHealthTool(newTestConfig(), cs)
	res, err := tool.Call(t.Context(), nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	got := parseHealth(t, res.Content[0].Text)
	components := mustMap(t, got["components"])
	webhook := mustMap(t, components["webhook"])
	if webhook["status"] != "absent" {
		t.Errorf("webhook status: got %v, want absent", webhook["status"])
	}
	if webhook["kind"] != "MutatingWebhookConfiguration" {
		t.Errorf("webhook kind: got %v, want MutatingWebhookConfiguration", webhook["kind"])
	}
}

func TestEoBHealth_AgentPodsByNodeAggregatesPerNode(t *testing.T) {
	t.Parallel()
	cfg := newTestConfig()
	mkPod := func(name, node string, isReady bool) *corev1.Pod {
		cond := corev1.ConditionFalse
		if isReady {
			cond = corev1.ConditionTrue
		}
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: cfg.TawonNamespace,
				Labels:    map[string]string{"app.kubernetes.io/name": "tawon-directive"},
			},
			Spec: corev1.PodSpec{NodeName: node},
			Status: corev1.PodStatus{
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: cond}},
			},
		}
	}
	cs := fake.NewSimpleClientset(
		mkPod("foo-a", "master-0", true),
		mkPod("bar-a", "master-0", true),
		mkPod("foo-b", "master-1", true),
		mkPod("bar-b", "master-1", false),
	)
	tool := newHealthTool(cfg, cs)
	res, err := tool.Call(t.Context(), nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	got := parseHealth(t, res.Content[0].Text)
	byNode := mustMap(t, got["agents_per_node"])
	m0 := mustMap(t, byNode["master-0"])
	if m0["ready"].(float64) != 2 || m0["total"].(float64) != 2 {
		t.Errorf("master-0: got ready=%v total=%v, want 2/2", m0["ready"], m0["total"])
	}
	m1 := mustMap(t, byNode["master-1"])
	if m1["ready"].(float64) != 1 || m1["total"].(float64) != 2 {
		t.Errorf("master-1: got ready=%v total=%v, want 1/2", m1["ready"], m1["total"])
	}
}

// newDirectiveDS returns a fake DaemonSet labeled to match the default
// DirectiveLabelSelector and configured with the given ready/desired
// counts on its Status block.
func newDirectiveDS(ns, name string, desired, ready int32) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    map[string]string{"app.kubernetes.io/name": "tawon-directive"},
		},
		Status: appsv1.DaemonSetStatus{
			DesiredNumberScheduled: desired,
			NumberReady:            ready,
		},
	}
}

func parseHealth(t *testing.T, body string) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal health: %v\nbody=%s", err, body)
	}
	return got
}
