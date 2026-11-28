package chatd

// This file is fork code: the fork-compaction-recovery experiment flag that
// compaction options carry into the chatloop. turn_environment.go and the
// compaction override path in generation.go call forkSummaryRecovery, one
// hook line in each upstream file.

import "github.com/coder/coder/v2/codersdk"

// forkSummaryRecovery reports whether the fork-compaction-recovery experiment
// is enabled. The compaction options carry it, so a summary stream that spent
// its whole output cap on reasoning and returned no text retries once with
// reasoning at low effort instead of failing the compaction. Without the
// experiment the flag is false and compaction behaves exactly as upstream.
func (p *Server) forkSummaryRecovery() bool {
	return p.experiments.Enabled(codersdk.ExperimentForkCompactionRecovery)
}
