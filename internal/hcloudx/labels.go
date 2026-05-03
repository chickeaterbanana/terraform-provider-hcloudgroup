// Package hcloudx wraps the Hetzner Cloud SDK with provider-specific
// helpers: label-based discovery, action waiting, read-modify-write of the
// labels map, and retry classification.
package hcloudx

// Reserved label namespace. Operator-supplied labels with this prefix are
// rejected at plan time; the provider owns this prefix exclusively.
const Namespace = "hcloudgroup.io/"

// Reserved label keys (spec section 5.2).
const (
	LabelManagedBy   = Namespace + "managed-by"
	LabelGroup       = Namespace + "group"
	LabelSlot        = Namespace + "slot"
	LabelGeneration  = Namespace + "generation"
	LabelReplaceHash = Namespace + "replace-hash"
	LabelComplete    = Namespace + "complete"
)

// ManagedByValue is the constant value of LabelManagedBy. The selector
// "managed-by=ManagedByValue" identifies provider-managed servers.
const ManagedByValue = "hcloudgroup-provider"

// SplitUserAndProvider partitions a server's labels into the user-visible
// subset (returned as user) and the provider-internal subset (returned as
// provider). Read uses this to populate the resource's labels attribute
// without leaking the hcloudgroup.io/* keys back into HCL state.
func SplitUserAndProvider(all map[string]string) (user map[string]string, provider map[string]string) {
	user = make(map[string]string, len(all))
	provider = make(map[string]string, len(all))
	for k, v := range all {
		if HasNamespace(k) {
			provider[k] = v
		} else {
			user[k] = v
		}
	}
	return user, provider
}

// HasNamespace reports whether key is in the reserved provider namespace.
func HasNamespace(key string) bool {
	return len(key) >= len(Namespace) && key[:len(Namespace)] == Namespace
}

// MergeForCreate combines user labels with the full set of provider labels
// for an initial server creation, returning a fresh map. The provider sets
// complete=false at create time; the flip to true happens in a separate
// UpdateLabels call after every post-action has succeeded.
func MergeForCreate(userLabels map[string]string, group string, slotID, generation int, replaceHashPrefix string) map[string]string {
	merged := make(map[string]string, len(userLabels)+6)
	for k, v := range userLabels {
		if !HasNamespace(k) {
			merged[k] = v
		}
	}
	merged[LabelManagedBy] = ManagedByValue
	merged[LabelGroup] = group
	merged[LabelSlot] = itoa(slotID)
	merged[LabelGeneration] = itoa(generation)
	merged[LabelReplaceHash] = replaceHashPrefix
	merged[LabelComplete] = "false"
	return merged
}
