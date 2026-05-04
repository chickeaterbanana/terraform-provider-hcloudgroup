package reconciler

import "fmt"

// rfc1123MaxLen mirrors the schema validator's constant. Duplicated to
// avoid an import cycle (this package can't import servergroup).
const rfc1123MaxLen = 63

// ServerName is the deterministic hostname assigned at server creation.
// Consumers must validate that len("group-slot-generation") <= 63 at plan
// time so this never collides with RFC 1123. The schema validator caps
// group names and replicas assuming a worst-case 6-digit generation budget.
//
// As defense in depth, panic if the budget is exceeded — reaching here
// means a contract bug between the schema validators and this function;
// silently producing an over-long name would leave the operator looking
// at an opaque hcloud 4xx mid-apply.
func ServerName(group string, slot, generation int) string {
	name := fmt.Sprintf("%s-%d-%d", group, slot, generation)
	if len(name) > rfc1123MaxLen {
		panic(fmt.Sprintf("ServerName overflow: %q (%d chars, RFC 1123 max %d) — schema validators should have rejected this at plan time", name, len(name), rfc1123MaxLen))
	}
	return name
}
