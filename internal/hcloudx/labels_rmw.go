package hcloudx

import "context"

// SetProviderLabel performs the read-modify-write needed to change a single
// provider-namespaced label without disturbing user labels or the rest of
// the provider's labels.
//
// Hcloud's Server.Update replaces the labels map wholesale. Single-writer
// safety comes from terraform's state lock - no two applies can race
// against the same group.
func SetProviderLabel(ctx context.Context, c Client, serverID int64, key, value string) error {
	srv, err := c.GetServer(ctx, serverID)
	if err != nil {
		return err
	}
	merged := make(map[string]string, len(srv.Labels)+1)
	for k, v := range srv.Labels {
		merged[k] = v
	}
	merged[key] = value
	_, err = c.UpdateServerLabels(ctx, serverID, merged)
	return err
}
