package tools

import (
	"encoding/json"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestEoBHealth_NoKubeReturnsStub(t *testing.T) {
	t.Parallel()
	tool := NewEoBHealth(newTestConfig(), nil)
	res, err := tool.Call(t.Context(), nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	got := parseHealth(t, res.Content[0].Text)
	if got["status"] != "no-cluster" {
		t.Errorf("status: got %v, want %q", got["status"], "no-cluster")
	}
}

func TestEoBHealth_AllComponentsAbsentWhenEmptyCluster(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset()
	tool := NewEoBHealth(newTestConfig(), cs)
	res, err := tool.Call(t.Context(), nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	got := parseHealth(t, res.Content[0].Text)
	for _, key := range []string{"operator", "dashboard", "streamstore", "webhook", "agent"} {
		comp := got[key].(map[string]any)
		if comp["status"] != "absent" {
			t.Errorf("%s: status=%v, want absent", key, comp["status"])
		}
	}
}

func TestEoBHealth_HealthyDeploymentsReportOK(t *testing.T) {
	t.Parallel()
	cfg := newTestConfig()
	one := int32(1)
	two := int32(2)
	cs := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "tawon-operator", Namespace: cfg.OperatorNamespace},
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
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "tawon-webhook", Namespace: cfg.TawonNamespace},
			Spec:       appsv1.DeploymentSpec{Replicas: &one},
			Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
		},
		&appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Name: "tawon-agent", Namespace: cfg.TawonNamespace},
			Status:     appsv1.DaemonSetStatus{DesiredNumberScheduled: 3, NumberReady: 3},
		},
	)
	tool := NewEoBHealth(cfg, cs)
	res, err := tool.Call(t.Context(), nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	got := parseHealth(t, res.Content[0].Text)
	for _, key := range []string{"operator", "dashboard", "streamstore", "webhook", "agent"} {
		comp := got[key].(map[string]any)
		if comp["status"] != "ok" {
			t.Errorf("%s: status=%v, want ok", key, comp["status"])
		}
	}
}

func TestEoBHealth_DegradedDaemonSet(t *testing.T) {
	t.Parallel()
	cfg := newTestConfig()
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "tawon-agent", Namespace: cfg.TawonNamespace},
		Status:     appsv1.DaemonSetStatus{DesiredNumberScheduled: 3, NumberReady: 2},
	}
	cs := fake.NewSimpleClientset(ds)
	tool := NewEoBHealth(cfg, cs)
	res, err := tool.Call(t.Context(), nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	got := parseHealth(t, res.Content[0].Text)
	agent := got["agent"].(map[string]any)
	if agent["status"] != "degraded" {
		t.Errorf("agent status: got %v, want degraded", agent["status"])
	}
}

func TestEoBHealth_AgentPodsByNode(t *testing.T) {
	t.Parallel()
	cfg := newTestConfig()
	ready := func(name, node string, isReady bool) *corev1.Pod {
		cond := corev1.ConditionFalse
		if isReady {
			cond = corev1.ConditionTrue
		}
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: cfg.TawonNamespace,
				Labels:    map[string]string{"app": "tawon-agent"},
			},
			Spec: corev1.PodSpec{NodeName: node},
			Status: corev1.PodStatus{
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: cond}},
			},
		}
	}
	cs := fake.NewSimpleClientset(
		ready("agent-0", "master-0", true),
		ready("agent-1", "master-1", true),
		ready("agent-2", "master-2", false),
	)
	tool := NewEoBHealth(cfg, cs)
	res, err := tool.Call(t.Context(), nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	got := parseHealth(t, res.Content[0].Text)
	byNode, ok := got["agents_per_node"].(map[string]any)
	if !ok {
		t.Fatalf("agents_per_node: type=%T, want map", got["agents_per_node"])
	}
	if byNode["master-0"] != "Ready" {
		t.Errorf("master-0: got %v, want Ready", byNode["master-0"])
	}
	if byNode["master-2"] != "NotReady" {
		t.Errorf("master-2: got %v, want NotReady", byNode["master-2"])
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
