package reconciler

import "fmt"

// SlotError carries enough context for the resource layer to surface a
// useful tofu diagnostic. Phase identifies *which* hook failed so the
// operator doesn't have to guess from the message alone.
type SlotError struct {
	SlotID int
	Phase  string
	Cause  error
	Stdout string
	Stderr string
}

func (e *SlotError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("slot %d %s: %v", e.SlotID, e.Phase, e.Cause)
}

func (e *SlotError) Unwrap() error { return e.Cause }
