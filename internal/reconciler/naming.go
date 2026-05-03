package reconciler

import "fmt"

// ServerName is the deterministic hostname assigned at server creation.
// Consumers must validate that len("group-slot-generation") <= 63 at plan
// time so this never collides with RFC 1123. The schema validator caps
// group names assuming a worst-case 6-digit generation budget.
func ServerName(group string, slot, generation int) string {
	return fmt.Sprintf("%s-%d-%d", group, slot, generation)
}
