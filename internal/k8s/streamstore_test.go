package k8s

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDiscoverStreamStoreURL(t *testing.T) {
	tests := []struct {
		name    string
		svcs    []corev1.Service
		ns      string
		want    string
		wantErr bool
	}{
		{
			name: "matched by label",
			svcs: []corev1.Service{
				newService("tawon-operator", "tawon-streamstore-d2f18e",
					map[string]string{"app": "tawon-streamstore"}),
				newService("tawon-operator", "tawon-dashboard", nil),
			},
			ns:   "tawon-operator",
			want: "nats://tawon-streamstore-d2f18e.tawon-operator.svc:4222",
		},
		{
			name: "matched by name pattern when label absent",
			svcs: []corev1.Service{
				newService("tawon-operator", "tawon-streamstore-abcdef", nil),
				newService("tawon-operator", "tawon-operator-controller-manager", nil),
			},
			ns:   "tawon-operator",
			want: "nats://tawon-streamstore-abcdef.tawon-operator.svc:4222",
		},
		{
			name: "name pattern requires hex suffix",
			svcs: []corev1.Service{
				newService("tawon-operator", "tawon-streamstore", nil),
			},
			ns:   "tawon-operator",
			want: "",
		},
		{
			name: "no streamstore present",
			svcs: []corev1.Service{
				newService("tawon-operator", "tawon-dashboard", nil),
			},
			ns:   "tawon-operator",
			want: "",
		},
		{
			name: "namespace filters out matches",
			svcs: []corev1.Service{
				newService("other", "tawon-streamstore-d2f18e",
					map[string]string{"app": "tawon-streamstore"}),
			},
			ns:   "tawon-operator",
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			objs := make([]runtime.Object, 0, len(tc.svcs))
			for i := range tc.svcs {
				objs = append(objs, &tc.svcs[i])
			}
			cs := fake.NewSimpleClientset(objs...)
			c := &Client{Clientset: cs}
			got, err := c.DiscoverStreamStoreURL(context.Background(), tc.ns)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("url=%q, want %q", got, tc.want)
			}
		})
	}
}

func newService(ns, name string, labels map[string]string) corev1.Service {
	return corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      name,
			Labels:    labels,
		},
	}
}
