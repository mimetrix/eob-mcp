package service

import "testing"

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
