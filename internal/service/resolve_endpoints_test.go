package service

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
)

func TestResolveEndpoints(t *testing.T) {
	ctrl := true
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "shop", Name: "checkout-7d9c-abc",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "checkout-7d9c5f", Controller: &ctrl},
			},
		},
		Spec:   corev1.PodSpec{ServiceAccountName: "checkout-sa"},
		Status: corev1.PodStatus{PodIP: "10.1.2.3"},
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "kubernetes"},
		Spec:       corev1.ServiceSpec{ClusterIP: "10.3.0.1"},
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "master-0"},
		Status:     corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "172.31.44.247"}}},
	}
	cs := fake.NewSimpleClientset(pod, svc, node)
	srv := New(testHealthConfig(), cs, nil, nil)

	req := &eobv1.ResolveEndpointsRequest{Endpoints: []*eobv1.Endpoint{
		{Ip: "10.1.2.3", Port: 8080},
		{Ip: "10.3.0.1", Port: 443},
		{Ip: "172.31.44.247", Port: 22},
		{Ip: "8.8.8.8", Port: 53},
	}}
	resp, err := srv.ResolveEndpoints(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 4 {
		t.Fatalf("results=%d, want 4", len(resp.Results))
	}
	// pod: workload collapsed RS->Deployment, SA carried
	p := resp.Results[0]
	if p.Kind != "pod" || p.Namespace != "shop" || p.Workload != "checkout" || p.WorkloadKind != "Deployment" || p.ServiceAccount != "checkout-sa" {
		t.Errorf("pod resolve wrong: %+v", p)
	}
	if resp.Results[1].Kind != "service" || resp.Results[1].Name != "kubernetes" {
		t.Errorf("service resolve wrong: %+v", resp.Results[1])
	}
	if resp.Results[2].Kind != "node" || resp.Results[2].Name != "master-0" {
		t.Errorf("node resolve wrong: %+v", resp.Results[2])
	}
	if resp.Results[3].Kind != "external" {
		t.Errorf("external resolve wrong: %+v", resp.Results[3])
	}
}

func TestResolveEndpoints_NoKube(t *testing.T) {
	srv := New(testHealthConfig(), nil, nil, nil)
	resp, err := srv.ResolveEndpoints(context.Background(), &eobv1.ResolveEndpointsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetClusterState() != "no-cluster" {
		t.Errorf("ClusterState=%q, want no-cluster", resp.GetClusterState())
	}
}
