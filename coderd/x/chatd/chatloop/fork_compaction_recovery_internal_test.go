package chatloop

// This file is fork code: tests of the fork-compaction-recovery hook.

import (
	"context"
	"strings"
	"testing"

	"charm.land/fantasy"
	fantasyanthropic "charm.land/fantasy/providers/anthropic"
	fantasyopenai "charm.land/fantasy/providers/openai"
	fantasyopenaicompat "charm.land/fantasy/providers/openaicompat"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/util/ptr"
	"github.com/coder/coder/v2/coderd/x/chatd/chattest"
	"github.com/coder/coder/v2/testutil"
)

// truncatedReasoningStream is the failure the issue reports: a reasoning
// model spends the whole output cap on reasoning, produces no text, and
// finishes at the length cap.
func truncatedReasoningStream() fantasy.StreamResponse {
	return compactionStream(
		fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningStart, ID: "reasoning"},
		fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningDelta, ID: "reasoning", Delta: "thinking"},
		fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningEnd, ID: "reasoning"},
		fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonLength},
	)
}

// recoveryOptions returns the options of a summary call for a reasoning
// model, the shape production gives compactionSummaryCall when a reasoning
// effort is configured. The FakeModel ignores them, but the hook needs a
// lowerable effort to build a retry.
func recoveryOptions() GenerateCompactionOptions {
	effort := fantasyopenai.ReasoningEffortHigh
	return GenerateCompactionOptions{
		SummaryPrompt:       "summarize",
		ForkSummaryRecovery: true,
		SummaryCall: fantasy.Call{
			ProviderOptions: fantasy.ProviderOptions{
				fantasyopenai.Name: &fantasyopenai.ProviderOptions{ReasoningEffort: &effort},
			},
		},
	}
}

// TestForkCompactionSummaryRecovery covers the hook compaction.go calls in its
// summary request loop. With the experiment on, a summary stream that spent
// the output cap on reasoning and returned no text retries once with the
// provider options lowered to low effort and returns the retry's text. With
// the experiment off the truncation fails the compaction terminally, exactly
// as upstream.
func TestForkCompactionSummaryRecovery(t *testing.T) {
	t.Parallel()

	t.Run("recovers on retry", func(t *testing.T) {
		t.Parallel()

		var calls []fantasy.Call
		model := &chattest.FakeModel{
			ProviderName: "fake",
			ModelName:    "fake-model",
			StreamFn: func(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
				calls = append(calls, call)
				if len(calls) == 1 {
					return truncatedReasoningStream(), nil
				}
				return summaryTextStream(), nil
			},
		}
		logSink := testutil.NewFakeSink(t)
		logger := logSink.Logger()
		opts := recoveryOptions()
		opts.Logger = logger

		summary, _, err := generateCompactionSummary(context.Background(), model, nil, opts)
		require.NoError(t, err)
		require.Equal(t, "summary", summary)
		require.Len(t, calls, 2)
		// The retry lowers the effort and keeps everything else.
		low := fantasyopenai.ReasoningEffortLow
		retryOpts, ok := calls[1].ProviderOptions[fantasyopenai.Name].(*fantasyopenai.ProviderOptions)
		require.True(t, ok)
		require.NotNil(t, retryOpts.ReasoningEffort)
		require.Equal(t, low, *retryOpts.ReasoningEffort)
		require.Equal(t, calls[0].MaxOutputTokens, calls[1].MaxOutputTokens,
			"the output cap stays as configured")
		// The first call still carries the configured effort.
		firstOpts, ok := calls[0].ProviderOptions[fantasyopenai.Name].(*fantasyopenai.ProviderOptions)
		require.True(t, ok)
		high := fantasyopenai.ReasoningEffortHigh
		require.NotNil(t, firstOpts.ReasoningEffort)
		require.Equal(t, high, *firstOpts.ReasoningEffort)
	})

	t.Run("experiment off fails terminally", func(t *testing.T) {
		t.Parallel()

		calls := 0
		model := &chattest.FakeModel{
			ProviderName: "fake",
			ModelName:    "fake-model",
			StreamFn: func(context.Context, fantasy.Call) (fantasy.StreamResponse, error) {
				calls++
				return truncatedReasoningStream(), nil
			},
		}

		summary, _, err := generateCompactionSummary(context.Background(), model, nil, GenerateCompactionOptions{})
		require.Empty(t, summary)
		require.EqualError(t, err, "compaction summary was truncated at the output token cap")
		require.Equal(t, 1, calls, "without the experiment there is no retry")
	})

	t.Run("retry still truncated fails terminally", func(t *testing.T) {
		t.Parallel()

		calls := 0
		model := &chattest.FakeModel{
			ProviderName: "fake",
			ModelName:    "fake-model",
			StreamFn: func(context.Context, fantasy.Call) (fantasy.StreamResponse, error) {
				calls++
				return truncatedReasoningStream(), nil
			},
		}
		logSink := testutil.NewFakeSink(t)
		opts := recoveryOptions()
		opts.Logger = logSink.Logger()

		summary, _, err := generateCompactionSummary(context.Background(), model, nil, opts)
		require.Empty(t, summary)
		require.EqualError(t, err, "compaction summary was truncated at the output token cap")
		require.Equal(t, 2, calls, "recovery retries exactly once")
	})

	t.Run("does not retry other errors", func(t *testing.T) {
		t.Parallel()

		streamErr := &fantasy.Error{Message: "provider is down"}
		calls := 0
		model := &chattest.FakeModel{
			ProviderName: "fake",
			ModelName:    "fake-model",
			StreamFn: func(context.Context, fantasy.Call) (fantasy.StreamResponse, error) {
				calls++
				return nil, streamErr
			},
		}

		summary, _, err := generateCompactionSummary(context.Background(), model, nil, GenerateCompactionOptions{
			SummaryPrompt:       "summarize",
			ForkSummaryRecovery: true,
		})
		require.Empty(t, summary)
		require.ErrorIs(t, err, streamErr)
		require.Equal(t, 1, calls, "only the truncation error retries")
	})

	t.Run("does not retry at low effort", func(t *testing.T) {
		t.Parallel()

		effort := fantasyopenai.ReasoningEffortLow
		calls := 0
		model := &chattest.FakeModel{
			ProviderName: "fake",
			ModelName:    "fake-model",
			StreamFn: func(context.Context, fantasy.Call) (fantasy.StreamResponse, error) {
				calls++
				return truncatedReasoningStream(), nil
			},
		}

		_, _, err := generateCompactionSummary(context.Background(), model, nil, GenerateCompactionOptions{
			SummaryPrompt:       "summarize",
			ForkSummaryRecovery: true,
			SummaryCall: fantasy.Call{
				ProviderOptions: fantasy.ProviderOptions{
					fantasyopenai.Name: &fantasyopenai.ProviderOptions{ReasoningEffort: &effort},
				},
			},
		})
		require.EqualError(t, err, "compaction summary was truncated at the output token cap")
		require.Equal(t, 1, calls, "a call already at low effort cannot spend less reasoning")
	})

	t.Run("empty retry text fails terminally", func(t *testing.T) {
		t.Parallel()

		calls := 0
		model := &chattest.FakeModel{
			ProviderName: "fake",
			ModelName:    "fake-model",
			StreamFn: func(context.Context, fantasy.Call) (fantasy.StreamResponse, error) {
				calls++
				if calls == 1 {
					return truncatedReasoningStream(), nil
				}
				// A retry that also produces no text re-raises the
				// truncation error rather than committing whitespace.
				return compactionStream(
					fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "text"},
					fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "text", Delta: "   "},
					fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "text"},
					fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop},
				), nil
			},
		}
		logSink := testutil.NewFakeSink(t)
		opts := recoveryOptions()
		opts.Logger = logSink.Logger()

		summary, _, err := generateCompactionSummary(context.Background(), model, nil, opts)
		require.Empty(t, summary)
		require.EqualError(t, err, "compaction summary was truncated at the output token cap")
		require.Equal(t, 2, calls)
	})
}

// TestForkCompactionRecoveryKeepsUpstreamErrors pins that the hook passes
// every non-truncation result through, including a successful summary and a
// context-too-large rejection that the thinner path owns.
func TestForkCompactionRecoveryKeepsUpstreamErrors(t *testing.T) {
	t.Parallel()

	t.Run("passes successful summary", func(t *testing.T) {
		t.Parallel()

		calls := 0
		model := &chattest.FakeModel{
			ProviderName: "fake",
			ModelName:    "fake-model",
			StreamFn: func(context.Context, fantasy.Call) (fantasy.StreamResponse, error) {
				calls++
				return summaryTextStream(), nil
			},
		}
		summary, _, err := generateCompactionSummary(context.Background(), model, nil, GenerateCompactionOptions{
			SummaryPrompt:       "summarize",
			ForkSummaryRecovery: true,
		})
		require.NoError(t, err)
		require.Equal(t, "summary", summary)
		require.Equal(t, 1, calls)
	})

	t.Run("lets context-too-large rejection reach the thinner", func(t *testing.T) {
		t.Parallel()

		calls := 0
		model := &chattest.FakeModel{
			ProviderName: "fake",
			ModelName:    "fake-model",
			StreamFn: func(_ context.Context, _ fantasy.Call) (fantasy.StreamResponse, error) {
				calls++
				if calls == 1 {
					return nil, contextWindowError()
				}
				return summaryTextStream(), nil
			},
		}
		logSink := testutil.NewFakeSink(t)

		summary, _, err := generateCompactionSummary(context.Background(), model,
			toolResultHistory(strings.Repeat("a", 100_000)),
			GenerateCompactionOptions{
				SummaryPrompt:       "summarize",
				ForkSummaryRecovery: true,
				ToolDefinitions:     []fantasy.Tool{fantasy.FunctionTool{Name: "read_file"}},
				Logger:              logSink.Logger(),
			},
		)
		require.NoError(t, err)
		require.Equal(t, "summary", summary)
		require.Equal(t, 2, calls, "the thinner retries the smaller request")
	})
}

// TestForkSummaryRecoveryCall covers the retry call construction for each
// provider shape chatd configures: the effort lowers to low, the rest of the
// call stays, and a call without a lowerable effort reports false.
func TestForkSummaryRecoveryCall(t *testing.T) {
	t.Parallel()

	low := fantasyopenai.ReasoningEffortLow
	none := fantasyopenai.ReasoningEffortNone
	high := fantasyopenai.ReasoningEffortHigh
	anthropicLow := fantasyanthropic.EffortLow
	anthropicNone := fantasyanthropic.EffortNone
	anthropicHigh := fantasyanthropic.EffortHigh

	t.Run("openai chat completions lowers high to low", func(t *testing.T) {
		t.Parallel()

		base := fantasy.Call{
			MaxOutputTokens: ptr.Ref(int64(32000)),
			ProviderOptions: fantasy.ProviderOptions{
				fantasyopenai.Name: &fantasyopenai.ProviderOptions{ReasoningEffort: &high},
			},
		}
		retry, ok := forkSummaryRecoveryCall(base)
		require.True(t, ok)
		require.Equal(t, base.MaxOutputTokens, retry.MaxOutputTokens)
		opts, ok := retry.ProviderOptions[fantasyopenai.Name].(*fantasyopenai.ProviderOptions)
		require.True(t, ok)
		require.NotNil(t, opts.ReasoningEffort)
		require.Equal(t, low, *opts.ReasoningEffort)
		// The original call must stay untouched.
		original, ok := base.ProviderOptions[fantasyopenai.Name].(*fantasyopenai.ProviderOptions)
		require.True(t, ok)
		require.Equal(t, high, *original.ReasoningEffort)
	})

	t.Run("openai responses lowers high to low", func(t *testing.T) {
		t.Parallel()

		base := fantasy.Call{
			ProviderOptions: fantasy.ProviderOptions{
				fantasyopenai.Name: &fantasyopenai.ResponsesProviderOptions{ReasoningEffort: &high},
			},
		}
		retry, ok := forkSummaryRecoveryCall(base)
		require.True(t, ok)
		opts, ok := retry.ProviderOptions[fantasyopenai.Name].(*fantasyopenai.ResponsesProviderOptions)
		require.True(t, ok)
		require.NotNil(t, opts.ReasoningEffort)
		require.Equal(t, low, *opts.ReasoningEffort)
	})

	t.Run("openaicompat lowers high to low", func(t *testing.T) {
		t.Parallel()

		base := fantasy.Call{
			ProviderOptions: fantasy.ProviderOptions{
				fantasyopenaicompat.Name: &fantasyopenaicompat.ProviderOptions{ReasoningEffort: &high},
			},
		}
		retry, ok := forkSummaryRecoveryCall(base)
		require.True(t, ok)
		opts, ok := retry.ProviderOptions[fantasyopenaicompat.Name].(*fantasyopenaicompat.ProviderOptions)
		require.True(t, ok)
		require.NotNil(t, opts.ReasoningEffort)
		require.Equal(t, low, *opts.ReasoningEffort)
	})

	t.Run("anthropic lowers high to low", func(t *testing.T) {
		t.Parallel()

		base := fantasy.Call{
			ProviderOptions: fantasy.ProviderOptions{
				fantasyanthropic.Name: &fantasyanthropic.ProviderOptions{Effort: &anthropicHigh},
			},
		}
		retry, ok := forkSummaryRecoveryCall(base)
		require.True(t, ok)
		opts, ok := retry.ProviderOptions[fantasyanthropic.Name].(*fantasyanthropic.ProviderOptions)
		require.True(t, ok)
		require.NotNil(t, opts.Effort)
		require.Equal(t, anthropicLow, *opts.Effort)
		// The original call must stay untouched.
		original, ok := base.ProviderOptions[fantasyanthropic.Name].(*fantasyanthropic.ProviderOptions)
		require.True(t, ok)
		require.Equal(t, anthropicHigh, *original.Effort)
	})

	t.Run("anthropic thinking without effort lowers", func(t *testing.T) {
		t.Parallel()

		base := fantasy.Call{
			ProviderOptions: fantasy.ProviderOptions{
				fantasyanthropic.Name: &fantasyanthropic.ProviderOptions{
					Thinking: &fantasyanthropic.ThinkingProviderOption{BudgetTokens: 8192},
				},
			},
		}
		retry, ok := forkSummaryRecoveryCall(base)
		require.True(t, ok)
		// The effort replaces the thinking configuration on the wire, so the
		// retry carries the low effort next to the untouched thinking field.
		opts, ok := retry.ProviderOptions[fantasyanthropic.Name].(*fantasyanthropic.ProviderOptions)
		require.True(t, ok)
		require.NotNil(t, opts.Effort)
		require.Equal(t, anthropicLow, *opts.Effort)
	})

	t.Run("no lowerable effort reports false", func(t *testing.T) {
		t.Parallel()

		for name, options := range map[string]fantasy.ProviderOptions{
			"openai at none": {
				fantasyopenai.Name: &fantasyopenai.ProviderOptions{ReasoningEffort: &none},
			},
			"anthropic at none": {
				fantasyanthropic.Name: &fantasyanthropic.ProviderOptions{Effort: &anthropicNone},
			},
			"no provider options": {},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, ok := forkSummaryRecoveryCall(fantasy.Call{ProviderOptions: options})
				require.False(t, ok)
			})
		}
	})
}
