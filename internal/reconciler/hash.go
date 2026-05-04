package reconciler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx"
)

// HashLabelLength is how many hex chars of the full SHA-256 are stored in
// the hcloudgroup.io/replace-hash label. The full hash lives in tofu state;
// the prefix is for at-a-glance debugging via `hcloud server list`.
const HashLabelLength = 12

// HashInputs is everything that contributes to the replace-hash. Changes
// to any field flip the hash and trigger a rolling replace of every slot.
//
// UserDataTemplate is the *raw* template source, not the rendered output -
// this means changes to peer state never cascade into a replace, only
// changes the operator wrote.
//
// Extras carries the values of attributes named in replace_on_change. The
// caller is responsible for resolving attribute names to their current
// values; the hashing step just consumes the {name -> serialized-value}
// map.
type HashInputs struct {
	Image            string
	ServerType       string
	UserDataTemplate string
	NetworkID        int64
	Location         string
	SSHKeys          []string
	Labels           map[string]string
	Extras           map[string]string
}

// canonicalForm normalizes h into a deterministic JSON-encodable structure.
// Maps become sorted slices of key/value pairs; user labels are filtered to
// drop any defensive collision in the provider namespace; SSH keys are
// sorted.
func (h HashInputs) canonicalForm() any {
	keys := append([]string(nil), h.SSHKeys...)
	sort.Strings(keys)

	labelPairs := make([][2]string, 0, len(h.Labels))
	for k, v := range h.Labels {
		if hcloudx.HasNamespace(k) {
			continue
		}
		labelPairs = append(labelPairs, [2]string{k, v})
	}
	sort.Slice(labelPairs, func(i, j int) bool {
		return labelPairs[i][0] < labelPairs[j][0]
	})

	extraPairs := make([][2]string, 0, len(h.Extras))
	for k, v := range h.Extras {
		extraPairs = append(extraPairs, [2]string{k, v})
	}
	sort.Slice(extraPairs, func(i, j int) bool {
		return extraPairs[i][0] < extraPairs[j][0]
	})

	return struct {
		Image            string      `json:"image"`
		ServerType       string      `json:"server_type"`
		UserDataTemplate string      `json:"user_data_template"`
		NetworkID        int64       `json:"network_id"`
		Location         string      `json:"location"`
		SSHKeys          []string    `json:"ssh_keys"`
		Labels           [][2]string `json:"labels"`
		Extras           [][2]string `json:"extras"`
	}{
		Image:            h.Image,
		ServerType:       h.ServerType,
		UserDataTemplate: h.UserDataTemplate,
		NetworkID:        h.NetworkID,
		Location:         h.Location,
		SSHKeys:          keys,
		Labels:           labelPairs,
		Extras:           extraPairs,
	}
}

// Hash returns the (full hex, 12-char prefix) tuple. Determinism is the
// only correctness property: same inputs across processes and Go versions
// must produce the same bytes.
func (h HashInputs) Hash() (full string, prefix string) {
	canonical := h.canonicalForm()
	b, err := json.Marshal(canonical)
	if err != nil {
		// json.Marshal of plain types/slices/strings cannot fail. If it
		// ever does, fail loud — a fallback string representation could
		// silently collapse two different inputs to the same hash and
		// hide a real replace.
		panic(fmt.Sprintf("hash: marshal canonical form: %v", err))
	}
	sum := sha256.Sum256(b)
	full = hex.EncodeToString(sum[:])
	prefix = full[:HashLabelLength]
	return full, prefix
}
