package k8s

import (
	"context"
	"fmt"
	"regexp"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// streamstoreLabelSelector matches the Service the Tawon chart renders
// for the JetStream StreamStore. The chart stamps both `app=tawon-streamstore`
// and `app.kubernetes.io/name=tawon-streamstore-<hex>` on the Service;
// the bare `app` label is stable across chart revisions, so it's the
// primary selector.
const streamstoreLabelSelector = "app=tawon-streamstore"

// streamstorePort is the JetStream client port the chart configures.
const streamstorePort = 4222

// streamstoreDiscoveryTimeout caps the kube API call so a slow apiserver
// at startup doesn't gate the entire server.
const streamstoreDiscoveryTimeout = 5 * time.Second

// streamstoreNamePattern is the fallback match if the chart-rendered
// Service drops the `app` label. Matches the chart-generated name
// `tawon-streamstore-<6+ hex chars>` exactly. The suffix is chart-stable
// per release but varies between releases — that variance is exactly why
// this discovery path exists.
var streamstoreNamePattern = regexp.MustCompile(`^tawon-streamstore-[a-f0-9]+$`)

// DiscoverStreamStoreURL looks up the chart-rendered streamstore Service
// in namespace and returns a `nats://<svc>.<ns>.svc:4222` endpoint
// suitable for JetStream dial.
//
// Returns "" with no error when no Service matches — the caller should
// treat that as "Stream* RPCs disabled," matching the legacy
// EOB_NATS_URL-unset path. Real API errors are returned as-is.
//
// Discovery walks label first (stable across reinstalls), then falls
// back to a name-pattern match if the label is absent on this chart
// revision.
func (c *Client) DiscoverStreamStoreURL(ctx context.Context, namespace string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, streamstoreDiscoveryTimeout)
	defer cancel()

	name, err := c.findStreamStoreName(ctx, namespace)
	if err != nil {
		return "", err
	}
	if name == "" {
		return "", nil
	}
	return fmt.Sprintf("nats://%s.%s.svc:%d", name, namespace, streamstorePort), nil
}

func (c *Client) findStreamStoreName(ctx context.Context, namespace string) (string, error) {
	svcs, err := c.Clientset.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: streamstoreLabelSelector,
	})
	if err != nil {
		return "", fmt.Errorf("list services in %s: %w", namespace, err)
	}
	if len(svcs.Items) > 0 {
		return svcs.Items[0].Name, nil
	}

	all, err := c.Clientset.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list services in %s: %w", namespace, err)
	}
	for i := range all.Items {
		if streamstoreNamePattern.MatchString(all.Items[i].Name) {
			return all.Items[i].Name, nil
		}
	}
	return "", nil
}
