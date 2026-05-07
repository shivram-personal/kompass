package search

import (
	"encoding/json"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/skyhook-io/radar/internal/k8s"
)

// objectActivation builds the top-level CEL binding map for a typed
// K8s object. Keys mirror the variable declarations in
// internal/filter/filter.go's envObject.
//
// We marshal the object once via JSON to convert typed structs into
// generic map[string]any — which is what CEL's dyn binding expects
// for nested fields (spec, status, metadata extras). Labels and
// annotations are extracted via meta.Accessor for typed access; the
// nested metadata blob is the JSON-marshaled form so deeper fields
// like metadata.creationTimestamp / ownerReferences remain reachable.
func objectActivation(obj runtime.Object, kind string) (map[string]any, error) {
	k8s.SetTypeMeta(obj)
	raw, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return assembleActivation(m, kind), nil
}

// unstructuredActivation builds the activation map for a CRD object.
// We already hold the map form — no marshal/unmarshal cost.
func unstructuredActivation(u *unstructured.Unstructured, kind string) map[string]any {
	if u == nil || u.Object == nil {
		return nil
	}
	return assembleActivation(u.Object, kind)
}

// assembleActivation projects the JSON-shaped object into the bound
// variable names. Keys missing from the object resolve to empty
// values so `has()` guards work as expected.
func assembleActivation(obj map[string]any, kind string) map[string]any {
	out := map[string]any{
		"kind":        firstString(obj["kind"], kind),
		"apiVersion":  asString(obj["apiVersion"]),
		"metadata":    asMap(obj["metadata"]),
		"spec":        asMap(obj["spec"]),
		"status":      asMap(obj["status"]),
		"labels":      asStringMap(getNested(obj, "metadata", "labels")),
		"annotations": asStringMap(getNested(obj, "metadata", "annotations")),
	}
	return out
}

// issueActivation builds the activation for an Issue-row CEL filter.
// Mirrors envIssue's variable declarations.
func issueActivation(severity, source, kind, group, namespace, name, reason, message, cluster string, count int, lastSeenUnix int64) map[string]any {
	return map[string]any{
		"severity":  severity,
		"source":    source,
		"kind":      kind,
		"group":     group,
		"namespace": namespace,
		"name":      name,
		"reason":    reason,
		"message":   message,
		"count":     int64(count),
		"cluster":   cluster,
		"last_seen": lastSeenUnix,
	}
}

func firstString(v any, fallback string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return fallback
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func getNested(obj map[string]any, keys ...string) any {
	cur := any(obj)
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[k]
	}
	return cur
}

func asStringMap(v any) map[string]string {
	switch m := v.(type) {
	case map[string]any:
		out := make(map[string]string, len(m))
		for k, val := range m {
			if s, ok := val.(string); ok {
				out[k] = s
			}
		}
		return out
	case map[string]string:
		return m
	}
	return map[string]string{}
}
