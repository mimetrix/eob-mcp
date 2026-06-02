package service

import (
	"context"
	"testing"

	admissionregv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/mimetrix/eob-mcp/internal/config"
	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

func TestEoBHealth_NoKube(t *testing.T) {
	srv := New(testHealthConfig(), nil, nil, nil)
	resp, err := srv.EoBHealth(context.Background(), &eobv1.EoBHealthRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetClusterState() != "no-cluster" {
		t.Errorf("ClusterState=%q, want no-cluster", resp.GetClusterState())
	}
	if len(resp.GetComponents()) != 0 {
		t.Errorf("Components=%v, want empty", resp.GetComponents())
	}
}

func TestEoBHealth_AllComponentsHealthy(t *testing.T) {
	cs := fake.NewSimpleClientset(
		readyDeployment("operators", "tawon-operator-controller-manager", 1, 1),
		readyDeployment("tawon-operator", tawonDashboardDeployment, 1, 1),
		readyStatefulSet("tawon-operator", tawonStreamstoreStatefulSet, 1, 1),
		oneWebhook("eob-mutate"),
	)
	srv := New(testHealthConfig(), cs, nil, nil)
	resp, err := srv.EoBHealth(context.Background(), &eobv1.EoBHealthRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetClusterState() != "connected" {
		t.Errorf("ClusterState=%q, want connected", resp.GetClusterState())
	}
	wantOK := []string{"operator", "dashboard", "streamstore", "webhook"}
	for _, key := range wantOK {
		got := resp.GetComponents()[key]
		if got == nil {
			t.Errorf("Components[%q] missing", key)
			continue
		}
		if got.GetStatus() != "ok" {
			t.Errorf("Components[%q].Status=%q, want ok", key, got.GetStatus())
		}
	}
	// No DaemonSets seeded → agent reports absent, directives is non-nil/empty.
	agent := resp.GetComponents()["agent"]
	if agent == nil || agent.GetStatus() != "absent" {
		t.Errorf("agent.Status=%v, want absent", agent)
	}
	if resp.GetDirectives() == nil {
		t.Errorf("Directives is nil, want non-nil (possibly empty)")
	}
}

func TestEoBHealth_MissingDeploymentReportsAbsent(t *testing.T) {
	cs := fake.NewSimpleClientset() // nothing seeded
	srv := New(testHealthConfig(), cs, nil, nil)
	resp, err := srv.EoBHealth(context.Background(), &eobv1.EoBHealthRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"operator", "dashboard", "streamstore", "webhook"} {
		got := resp.GetComponents()[key]
		if got == nil {
			t.Errorf("Components[%q] missing", key)
			continue
		}
		if got.GetStatus() != "absent" {
			t.Errorf("Components[%q].Status=%q, want absent", key, got.GetStatus())
		}
	}
}

func TestEoBHealth_DegradedDeployment(t *testing.T) {
	cs := fake.NewSimpleClientset(
		readyDeployment("operators", "tawon-operator-controller-manager", 3, 1), // desired 3, ready 1
	)
	srv := New(testHealthConfig(), cs, nil, nil)
	resp, err := srv.EoBHealth(context.Background(), &eobv1.EoBHealthRequest{})
	if err != nil {
		t.Fatal(err)
	}
	op := resp.GetComponents()["operator"]
	if op.GetStatus() != "degraded" {
		t.Errorf("operator.Status=%q, want degraded", op.GetStatus())
	}
	if op.GetReady() != 1 || op.GetDesired() != 3 {
		t.Errorf("operator ready/desired=%d/%d, want 1/3", op.GetReady(), op.GetDesired())
	}
}

func TestEoBHealth_DirectivesAggregatedAndSorted(t *testing.T) {
	cs := fake.NewSimpleClientset(
		readyDeployment("operators", "tawon-operator-controller-manager", 1, 1),
		// Two DaemonSets, both labeled — should appear in directive list.
		directiveDS("tawon-directive-bravo", 3, 3),
		directiveDS("tawon-directive-alpha", 3, 2), // degraded
		// Pods backing them, varying readiness.
		agentPod("tawon-directive-alpha-1", "master-0", true),
		agentPod("tawon-directive-alpha-2", "master-1", false),
		agentPod("tawon-directive-bravo-1", "master-0", true),
	)
	srv := New(testHealthConfig(), cs, nil, nil)
	resp, err := srv.EoBHealth(context.Background(), &eobv1.EoBHealthRequest{})
	if err != nil {
		t.Fatal(err)
	}
	dirs := resp.GetDirectives()
	if len(dirs) != 2 {
		t.Fatalf("Directives len=%d, want 2", len(dirs))
	}
	if dirs[0].GetName() != "tawon-directive-alpha" {
		t.Errorf("Directives[0].Name=%q, want tawon-directive-alpha (sorted)", dirs[0].GetName())
	}
	if dirs[0].GetStatus() != "degraded" {
		t.Errorf("alpha.Status=%q, want degraded", dirs[0].GetStatus())
	}
	if dirs[1].GetStatus() != "ok" {
		t.Errorf("bravo.Status=%q, want ok", dirs[1].GetStatus())
	}
	// Aggregate agent: 5 ready / 6 desired → degraded
	agent := resp.GetComponents()["agent"]
	if agent.GetReady() != 5 || agent.GetDesired() != 6 {
		t.Errorf("agent ready/desired=%d/%d, want 5/6", agent.GetReady(), agent.GetDesired())
	}
	if agent.GetStatus() != "degraded" {
		t.Errorf("agent.Status=%q, want degraded", agent.GetStatus())
	}
	// agentsPerNode: master-0 has 2 ready/2 total; master-1 has 0 ready/1 total.
	per := resp.GetAgentsPerNode()
	if per["master-0"].GetReady() != 2 || per["master-0"].GetTotal() != 2 {
		t.Errorf("master-0 ready/total=%d/%d, want 2/2", per["master-0"].GetReady(), per["master-0"].GetTotal())
	}
	if per["master-1"].GetReady() != 0 || per["master-1"].GetTotal() != 1 {
		t.Errorf("master-1 ready/total=%d/%d, want 0/1", per["master-1"].GetReady(), per["master-1"].GetTotal())
	}
}

func TestEoBHealth_PendingPodGroupedUnderSentinel(t *testing.T) {
	cs := fake.NewSimpleClientset(
		directiveDS("tawon-directive-x", 1, 0),
		agentPod("tawon-directive-x-1", "", false), // no NodeName
	)
	srv := New(testHealthConfig(), cs, nil, nil)
	resp, err := srv.EoBHealth(context.Background(), &eobv1.EoBHealthRequest{})
	if err != nil {
		t.Fatal(err)
	}
	per := resp.GetAgentsPerNode()
	if per["<pending>"] == nil || per["<pending>"].GetTotal() != 1 {
		t.Errorf("expected one pod under <pending>, got %v", per)
	}
}

func TestReadyStatus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ready, desired int32
		want           string
	}{
		{0, 0, "absent"},
		{1, 1, "ok"},
		{2, 1, "ok"},
		{0, 1, "degraded"},
		{1, 3, "degraded"},
	}
	for _, c := range cases {
		if got := readyStatus(c.ready, c.desired); got != c.want {
			t.Errorf("readyStatus(%d,%d)=%q, want %q", c.ready, c.desired, got, c.want)
		}
	}
}

// --- fixture helpers ---

func testHealthConfig() *config.Config {
	return &config.Config{
		SiteID:                 "test-site",
		Tenant:                 "test-tenant",
		Region:                 "test-region",
		OperatorNamespace:      "operators",
		TawonNamespace:         "tawon-operator",
		OperatorDeploymentName: "tawon-operator-controller-manager",
		WebhookConfigName:      "eob-mutate",
		DirectiveLabelSelector: "app.kubernetes.io/name=tawon-directive",
	}
}

func readyDeployment(ns, name string, desired, ready int32) *appsv1.Deployment {
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       appsv1.DeploymentSpec{Replicas: &desired},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: ready},
	}
	return d
}

func readyStatefulSet(ns, name string, desired, ready int32) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       appsv1.StatefulSetSpec{Replicas: &desired},
		Status:     appsv1.StatefulSetStatus{ReadyReplicas: ready},
	}
}

func oneWebhook(name string) *admissionregv1.MutatingWebhookConfiguration {
	return &admissionregv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks: []admissionregv1.MutatingWebhook{
			{Name: name + ".f5.local"},
		},
	}
}

func directiveDS(name string, desired, ready int32) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "tawon-operator",
			Name:      name,
			Labels:    map[string]string{"app.kubernetes.io/name": "tawon-directive"},
		},
		Status: appsv1.DaemonSetStatus{
			DesiredNumberScheduled: desired,
			NumberReady:            ready,
		},
	}
}

func agentPod(name, nodeName string, ready bool) *corev1.Pod {
	cond := corev1.ConditionFalse
	if ready {
		cond = corev1.ConditionTrue
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "tawon-operator",
			Name:      name,
			Labels:    map[string]string{"app.kubernetes.io/name": "tawon-directive"},
		},
		Spec: corev1.PodSpec{NodeName: nodeName},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: cond},
			},
		},
	}
}

// Silence unused warnings for runtime import (helper for type assertions
// in case future cases need it).
var _ runtime.Object = (*appsv1.Deployment)(nil)
