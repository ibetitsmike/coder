package forkdb

// The status values of a sandbox_operations row. The table's CHECK
// constraint allows exactly these.
const (
	// SandboxOperationStatusIntent is a written intent that no worker claimed.
	SandboxOperationStatusIntent = "intent"
	// SandboxOperationStatusClaimed means a worker drives the container
	// runtime for the operation.
	SandboxOperationStatusClaimed = "claimed"
	// SandboxOperationStatusDone means the runtime calls succeeded.
	SandboxOperationStatusDone = "done"
	// SandboxOperationStatusFailed means the runtime calls failed.
	SandboxOperationStatusFailed = "failed"
	// SandboxOperationStatusSuperseded means a newer build of the workspace
	// fenced the operation before it finished.
	SandboxOperationStatusSuperseded = "superseded"
)
