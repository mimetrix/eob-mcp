package service

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

func TestTraceHealth_NoKube(t *testing.T) {
	srv := New(testHealthConfig(), nil, nil, nil)
	resp, err := srv.TraceHealth(context.Background(), &eobv1.TraceHealthRequest{})
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

func TestTraceHealth_BothPresentHealthy(t *testing.T) {
	cs := fake.NewSimpleClientset(
		traceDS(traceNamespace, traceAgentDS, 3, 3),
		traceDS(raceNamespace, raceAgentDS, 3, 3),
		traceAgentPod(traceNamespace, "trace-agent-a", traceAgentDS, "master-0", true),
		traceAgentPod(traceNamespace, "trace-agent-b", traceAgentDS, "master-1", true),
		traceAgentPod(raceNamespace, "defense-agent-a", raceAgentDS, "master-0", true),
		traceAgentPod(raceNamespace, "defense-agent-b", raceAgentDS, "master-1", false),
	)
	srv := New(testHealthConfig(), cs, nil, nil)
	resp, err := srv.TraceHealth(context.Background(), &eobv1.TraceHealthRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetClusterState() != "connected" {
		t.Errorf("ClusterState=%q, want connected", resp.GetClusterState())
	}
	for _, key := range []string{"trace_agent", "race_agent"} {
		got := resp.GetComponents()[key]
		if got == nil || got.GetStatus() != "ok" {
			t.Errorf("Components[%q]=%v, want ok", key, got)
		}
	}
	// Per-node merged across both DaemonSets: master-0 = 2 ready/2 total
	// (trace ready + race ready); master-1 = 1 ready/2 total (trace ready,
	// race not-ready).
	per := resp.GetAgentsPerNode()
	if per["master-0"].GetReady() != 2 || per["master-0"].GetTotal() != 2 {
		t.Errorf("master-0=%d/%d, want 2/2", per["master-0"].GetReady(), per["master-0"].GetTotal())
	}
	if per["master-1"].GetReady() != 1 || per["master-1"].GetTotal() != 2 {
		t.Errorf("master-1=%d/%d, want 1/2", per["master-1"].GetReady(), per["master-1"].GetTotal())
	}
}

func TestTraceHealth_AbsentWhenNotInstalled(t *testing.T) {
	cs := fake.NewSimpleClientset() // no defense stack on this site
	srv := New(testHealthConfig(), cs, nil, nil)
	resp, err := srv.TraceHealth(context.Background(), &eobv1.TraceHealthRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetClusterState() != "connected" {
		t.Errorf("ClusterState=%q, want connected", resp.GetClusterState())
	}
	for _, key := range []string{"trace_agent", "race_agent"} {
		got := resp.GetComponents()[key]
		if got == nil || got.GetStatus() != "absent" {
			t.Errorf("Components[%q]=%v, want absent", key, got)
		}
	}
}

// --- fixtures ---

func traceDS(ns, name string, desired, ready int32) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
		},
		Status: appsv1.DaemonSetStatus{DesiredNumberScheduled: desired, NumberReady: ready},
	}
}

func traceAgentPod(ns, name, app, node string, ready bool) *corev1.Pod {
	cond := corev1.ConditionFalse
	if ready {
		cond = corev1.ConditionTrue
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: map[string]string{"app": app}},
		Spec:       corev1.PodSpec{NodeName: node},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: cond}},
		},
	}
}
