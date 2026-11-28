package chatloop

// This file is fork code: the fork-compaction-recovery retry of a compaction
// summary stream that spent its whole output cap on reasoning and returned no
// summary text. compaction.go's truncation branch calls forkRecoveredSummary,
// the one hook line in that upstream file.

import (
	"context"
	"strings"

	"charm.land/fantasy"
	fantasyanthropic "charm.land/fantasy/providers/anthropic"
	fantasyopenai "charm.land/fantasy/providers/openai"
	fantasyopenaicompat "charm.land/fantasy/providers/openaicompat"

	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
)

// errCompactionSummaryTruncated is the error a summary stream that produced no
// text after spending its whole output cap raises. It keeps the exact message
// upstream reports, and chaterror classifies it as a non-retryable generic
// error, so an unrecoverable truncation still fails the compaction terminally.
var errCompactionSummaryTruncated = xerrors.New("compaction summary was truncated at the output token cap")

// forkRecoveredSummary runs the fork-compaction-recovery hook of the summary
// request loop in compaction.go. It passes every result that is not the output
// cap truncation through unchanged, so upstream behavior is byte-identical.
// With the experiment off the truncation error comes back as is. With it on,
// the summary call retries once with reasoning at low effort and the same
// output cap: reasoning consumed the cap, so the summary text has room
// without it. Low is the one effort every provider shape chatd configures
// accepts, and a retry is skipped when the call already ran at low or below,
// because no smaller effort exists to try. A retry that fails, or that also
// returns no text, re-raises the truncation error so the compaction fails
// terminally exactly as it would upstream.
func forkRecoveredSummary(
	ctx context.Context,
	call fantasy.Call,
	options GenerateCompactionOptions,
	streamSummaryText func(fantasy.Call) (string, error),
	summary string,
	err error,
) (string, error) {
	if err == nil {
		return summary, nil
	}
	if err.Error() != errCompactionSummaryTruncated.Error() {
		return summary, err
	}
	if !options.ForkSummaryRecovery {
		return "", errCompactionSummaryTruncated
	}
	retryCall, ok := forkSummaryRecoveryCall(call)
	if !ok {
		return "", errCompactionSummaryTruncated
	}
	options.Logger.Warn(ctx, "compaction summary spent the output cap on reasoning, retrying at low effort",
		slog.F("provider", options.ResolvedProvider),
		slog.F("model", options.ResolvedModel),
	)
	text, retryErr := streamSummaryText(retryCall)
	if retryErr != nil || strings.TrimSpace(text) == "" {
		if retryErr != nil {
			options.Logger.Warn(ctx, "compaction summary recovery retry failed", slog.Error(retryErr))
		}
		return "", errCompactionSummaryTruncated
	}
	return text, nil
}

// forkSummaryRecoveryCall copies call with every reasoning knob it carries
// lowered to low. It reports false when the call carries no provider options
// that can lower reasoning, in which case a retry cannot help. The output cap
// stays as configured: the cap is the provider ceiling chatd knows, and
// providers reject a larger one.
func forkSummaryRecoveryCall(call fantasy.Call) (fantasy.Call, bool) {
	if len(call.ProviderOptions) == 0 {
		return call, false
	}
	options := make(fantasy.ProviderOptions, len(call.ProviderOptions))
	for name, data := range call.ProviderOptions {
		options[name] = data
	}
	changed := false
	if opts, ok := options[fantasyanthropic.Name].(*fantasyanthropic.ProviderOptions); ok && opts != nil && forkAnthropicEffortLowerable(*opts) {
		clone := *opts
		low := fantasyanthropic.EffortLow
		clone.Effort = &low
		options[fantasyanthropic.Name] = &clone
		changed = true
	}
	if opts, ok := options[fantasyopenai.Name].(*fantasyopenai.ProviderOptions); ok && opts != nil && forkOpenAIEffortLowerable(opts.ReasoningEffort) {
		clone := *opts
		low := fantasyopenai.ReasoningEffortLow
		clone.ReasoningEffort = &low
		options[fantasyopenai.Name] = &clone
		changed = true
	}
	if opts, ok := options[fantasyopenai.Name].(*fantasyopenai.ResponsesProviderOptions); ok && opts != nil && forkOpenAIEffortLowerable(opts.ReasoningEffort) {
		clone := *opts
		low := fantasyopenai.ReasoningEffortLow
		clone.ReasoningEffort = &low
		options[fantasyopenai.Name] = &clone
		changed = true
	}
	if opts, ok := options[fantasyopenaicompat.Name].(*fantasyopenaicompat.ProviderOptions); ok && opts != nil && forkOpenAIEffortLowerable(opts.ReasoningEffort) {
		clone := *opts
		low := fantasyopenai.ReasoningEffortLow
		clone.ReasoningEffort = &low
		options[fantasyopenaicompat.Name] = &clone
		changed = true
	}
	if !changed {
		return call, false
	}
	retry := call
	retry.ProviderOptions = options
	return retry, true
}

// forkOpenAIEffortLowerable reports whether an OpenAI or OpenAI-compatible
// reasoning effort is set and above low. Effort nil means the call carries no
// reasoning effort, so the retry cannot lower it. An effort already at none,
// minimal or low burned the cap once with the least reasoning available, so a
// retry at low cannot spend less.
func forkOpenAIEffortLowerable(effort *fantasyopenai.ReasoningEffort) bool {
	if effort == nil {
		return false
	}
	switch *effort {
	case fantasyopenai.ReasoningEffortNone,
		fantasyopenai.ReasoningEffortMinimal,
		fantasyopenai.ReasoningEffortLow:
		return false
	default:
		return true
	}
}

// forkAnthropicEffortLowerable reports whether the Anthropic summary options
// can retry with less reasoning. An effort already at none, minimal or low
// cannot. Thinking configured without an effort (a thinking budget) can: the
// retry's effort replaces the thinking configuration on the wire.
func forkAnthropicEffortLowerable(opts fantasyanthropic.ProviderOptions) bool {
	if opts.Effort == nil {
		return opts.Thinking != nil
	}
	switch *opts.Effort {
	case fantasyanthropic.EffortNone,
		fantasyanthropic.EffortMinimal,
		fantasyanthropic.EffortLow:
		return false
	default:
		return true
	}
}
