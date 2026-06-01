package tools

import (
	"context"
	"encoding/json"
	"fmt"

	eobv1 "github.com/mimetrix/eob-mcp/gen/go/eob/v1"
	"github.com/mimetrix/eob-mcp/internal/mcp"
	"github.com/mimetrix/eob-mcp/internal/service"
)

// ---------------- resource_list ----------------

// ResourceList is the MCP wrapper for EoBService.ResourceList. The
// implementation lives in internal/service/resource_list.go.
type ResourceList struct {
	svc *service.Server
}

func NewResourceList(svc *service.Server) *ResourceList {
	return &ResourceList{svc: svc}
}

func (t *ResourceList) Name() string { return "resource_list" }
func (t *ResourceList) Description() string {
	return "List Kubernetes resources of a given Kind. Defaults apiGroup to the Tawon EoB group (tawon.mantisnet.com) when not provided. Returns a slim summary including name, namespace, and any top-level status fields. Optional labelSelector filter."
}
func (t *ResourceList) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string","description":"Kubernetes Kind, e.g. ClusterDirective, Dashboard"},"apiGroup":{"type":"string","description":"API group; defaults to tawon.mantisnet.com"},"namespace":{"type":"string","description":"Namespace; ignored for cluster-scoped resources, defaults to all namespaces for namespaced"},"labelSelector":{"type":"string","description":"Standard k8s label selector"}},"required":["kind"],"additionalProperties":false}`)
}

type resourceListArgs struct {
	Kind          string `json:"kind"`
	APIGroup      string `json:"apiGroup"`
	Namespace     string `json:"namespace"`
	LabelSelector string `json:"labelSelector"`
}

func (t *ResourceList) Call(ctx context.Context, raw json.RawMessage) (mcp.CallToolResult, error) {
	var a resourceListArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return mcp.CallToolResult{}, fmt.Errorf("parse args: %w", err)
		}
	}
	resp, err := t.svc.ResourceList(ctx, &eobv1.ResourceListRequest{
		Kind:          a.Kind,
		ApiGroup:      a.APIGroup,
		Namespace:     a.Namespace,
		LabelSelector: a.LabelSelector,
	})
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	body, err := protoMarshal.Marshal(resp)
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	return mcp.CallToolResult{
		Content: []mcp.Content{{Type: "text", Text: string(body)}},
	}, nil
}

var _ mcp.ToolHandler = (*ResourceList)(nil)

// ---------------- resource_get ----------------

// ResourceGet is the MCP wrapper for EoBService.ResourceGet. The
// implementation lives in internal/service/resource_get.go.
type ResourceGet struct {
	svc *service.Server
}

func NewResourceGet(svc *service.Server) *ResourceGet {
	return &ResourceGet{svc: svc}
}

func (t *ResourceGet) Name() string { return "resource_get" }
func (t *ResourceGet) Description() string {
	return "Get one Kubernetes resource by Kind and name. Returns the full unstructured object including spec and status. Defaults apiGroup to tawon.mantisnet.com."
}
func (t *ResourceGet) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string"},"apiGroup":{"type":"string"},"namespace":{"type":"string"},"name":{"type":"string"}},"required":["kind","name"],"additionalProperties":false}`)
}

type resourceGetArgs struct {
	Kind      string `json:"kind"`
	APIGroup  string `json:"apiGroup"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

func (t *ResourceGet) Call(ctx context.Context, raw json.RawMessage) (mcp.CallToolResult, error) {
	var a resourceGetArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return mcp.CallToolResult{}, fmt.Errorf("parse args: %w", err)
		}
	}
	resp, err := t.svc.ResourceGet(ctx, &eobv1.ResourceGetRequest{
		Kind:      a.Kind,
		ApiGroup:  a.APIGroup,
		Namespace: a.Namespace,
		Name:      a.Name,
	})
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	body, err := protoMarshal.Marshal(resp)
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	return mcp.CallToolResult{
		Content: []mcp.Content{{Type: "text", Text: string(body)}},
	}, nil
}

var _ mcp.ToolHandler = (*ResourceGet)(nil)

// ---------------- resource_apply ----------------

// ResourceApply is the MCP wrapper for EoBService.ResourceApply. The
// implementation lives in internal/service/resource_apply.go.
type ResourceApply struct {
	svc *service.Server
}

func NewResourceApply(svc *service.Server) *ResourceApply {
	return &ResourceApply{svc: svc}
}

func (t *ResourceApply) Name() string { return "resource_apply" }
func (t *ResourceApply) Description() string {
	return "Apply a Kubernetes resource manifest via Server-Side Apply. Accepts YAML or JSON. Set dryRun=true to validate without persisting (server-side dry-run). Creates the resource if absent, updates it if present. Field manager defaults to 'eob-mcp'."
}
func (t *ResourceApply) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"manifest":{"type":"string","description":"Full resource manifest in YAML or JSON form"},"dryRun":{"type":"boolean","description":"If true, validate via server-side dry-run only"},"force":{"type":"boolean","description":"If true, take ownership of conflicting fields from other managers"}},"required":["manifest"],"additionalProperties":false}`)
}

type resourceApplyArgs struct {
	Manifest string `json:"manifest"`
	DryRun   bool   `json:"dryRun"`
	Force    bool   `json:"force"`
}

func (t *ResourceApply) Call(ctx context.Context, raw json.RawMessage) (mcp.CallToolResult, error) {
	var a resourceApplyArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return mcp.CallToolResult{}, fmt.Errorf("parse args: %w", err)
		}
	}
	resp, err := t.svc.ResourceApply(ctx, &eobv1.ResourceApplyRequest{
		Manifest: a.Manifest,
		DryRun:   a.DryRun,
		Force:    a.Force,
	})
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	body, err := protoMarshal.Marshal(resp)
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	return mcp.CallToolResult{
		Content: []mcp.Content{{Type: "text", Text: string(body)}},
	}, nil
}

var _ mcp.ToolHandler = (*ResourceApply)(nil)

// ---------------- resource_delete ----------------

// ResourceDelete is the MCP wrapper for EoBService.ResourceDelete. The
// implementation lives in internal/service/resource_delete.go.
type ResourceDelete struct {
	svc *service.Server
}

func NewResourceDelete(svc *service.Server) *ResourceDelete {
	return &ResourceDelete{svc: svc}
}

func (t *ResourceDelete) Name() string { return "resource_delete" }
func (t *ResourceDelete) Description() string {
	return "Delete a Kubernetes resource by Kind and name. Idempotent: returns status='notFound' if it's already gone. Defaults apiGroup to tawon.mantisnet.com."
}
func (t *ResourceDelete) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string"},"apiGroup":{"type":"string"},"namespace":{"type":"string"},"name":{"type":"string"}},"required":["kind","name"],"additionalProperties":false}`)
}

type resourceDeleteArgs struct {
	Kind      string `json:"kind"`
	APIGroup  string `json:"apiGroup"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

func (t *ResourceDelete) Call(ctx context.Context, raw json.RawMessage) (mcp.CallToolResult, error) {
	var a resourceDeleteArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return mcp.CallToolResult{}, fmt.Errorf("parse args: %w", err)
		}
	}
	resp, err := t.svc.ResourceDelete(ctx, &eobv1.ResourceDeleteRequest{
		Kind:      a.Kind,
		ApiGroup:  a.APIGroup,
		Namespace: a.Namespace,
		Name:      a.Name,
	})
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	body, err := protoMarshal.Marshal(resp)
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	return mcp.CallToolResult{
		Content: []mcp.Content{{Type: "text", Text: string(body)}},
	}, nil
}

var _ mcp.ToolHandler = (*ResourceDelete)(nil)

// ---------------- resource_schema ----------------

// ResourceSchema is the MCP wrapper for EoBService.ResourceSchema. The
// implementation lives in internal/service/resource_schema.go.
type ResourceSchema struct {
	svc *service.Server
}

func NewResourceSchema(svc *service.Server) *ResourceSchema {
	return &ResourceSchema{svc: svc}
}

func (t *ResourceSchema) Name() string { return "resource_schema" }
func (t *ResourceSchema) Description() string {
	return "Return the OpenAPI v3 schema for a CRD (used to generate or validate manifests). Looks up the CRD by group + kind. Useful before authoring a manifest to feed to resource_apply."
}
func (t *ResourceSchema) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string"},"apiGroup":{"type":"string"}},"required":["kind"],"additionalProperties":false}`)
}

type resourceSchemaArgs struct {
	Kind     string `json:"kind"`
	APIGroup string `json:"apiGroup"`
}

func (t *ResourceSchema) Call(ctx context.Context, raw json.RawMessage) (mcp.CallToolResult, error) {
	var a resourceSchemaArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return mcp.CallToolResult{}, fmt.Errorf("parse args: %w", err)
		}
	}
	resp, err := t.svc.ResourceSchema(ctx, &eobv1.ResourceSchemaRequest{
		Kind:     a.Kind,
		ApiGroup: a.APIGroup,
	})
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	body, err := protoMarshal.Marshal(resp)
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	return mcp.CallToolResult{
		Content: []mcp.Content{{Type: "text", Text: string(body)}},
	}, nil
}

var _ mcp.ToolHandler = (*ResourceSchema)(nil)
