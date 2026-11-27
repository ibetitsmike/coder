package forkdb

// The status values of a sandbox_operations row, as its CHECK constraint
// allows them.
const (
	SandboxOperationStatusIntent  = "intent"  // written, not claimed
	SandboxOperationStatusClaimed = "claimed" // a worker drives the runtime
	SandboxOperationStatusDone    = "done"
	SandboxOperationStatusFailed  = "failed"
	// SandboxOperationStatusSuperseded means a newer build fenced the
	// operation before it finished.
	SandboxOperationStatusSuperseded = "superseded"
	// SandboxOperationStatusReconciled means a newer generation finished as
	// done after the operation was superseded.
	SandboxOperationStatusReconciled = "reconciled"
)
