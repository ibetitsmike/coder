package agentcontext

// This file is fork code: it tests rule front-matter parsing.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseRule(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		want    ruleFrontmatter
		body    string
		errIs   error
		errText string
	}{
		{
			name:    "AlwaysApplyTrue",
			content: "---\ndescription: Go style\nalwaysApply: true\n---\n# Go\nUse gofmt.\n",
			want:    ruleFrontmatter{Description: "Go style", AlwaysApply: true},
			body:    "# Go\nUse gofmt.",
		},
		{
			name:    "AlwaysApplyFalseWithoutGlobs",
			content: "---\ndescription: On request\nalwaysApply: false\n---\nBody",
			want:    ruleFrontmatter{Description: "On request"},
			body:    "Body",
		},
		{
			name:    "BareGlobString",
			content: "---\nglobs: **/*.ts\nalwaysApply: false\n---\nTS rules",
			want:    ruleFrontmatter{Globs: []string{"**/*.ts"}},
			body:    "TS rules",
		},
		{
			name:    "CommaSeparatedGlobString",
			content: "---\nglobs: *.ts, src/**/*.tsx,\n---\nTS rules",
			want:    ruleFrontmatter{Globs: []string{"*.ts", "src/**/*.tsx"}},
			body:    "TS rules",
		},
		{
			name:    "QuotedGlobString",
			content: "---\nglobs: \"*.go\"\n---\nGo",
			want:    ruleFrontmatter{Globs: []string{"*.go"}},
			body:    "Go",
		},
		{
			name:    "FlowGlobList",
			content: "---\nglobs: [\"*.go\", \"go.mod\"]\n---\nGo",
			want:    ruleFrontmatter{Globs: []string{"*.go", "go.mod"}},
			body:    "Go",
		},
		{
			name:    "BlockGlobList",
			content: "---\nglobs:\n  - \"*.go\"\n  - \"{a,b}/*.md\"\n---\nGo",
			want:    ruleFrontmatter{Globs: []string{"*.go", "{a,b}/*.md"}},
			body:    "Go",
		},
		{
			name:    "EmptyGlobs",
			content: "---\nglobs:\nalwaysApply:\n---\nBody",
			body:    "Body",
		},
		{
			name:    "CRLFAndBOM",
			content: "\xef\xbb\xbf---\r\nalwaysApply: true\r\n---\r\nBody\r\n",
			want:    ruleFrontmatter{AlwaysApply: true},
			body:    "Body",
		},
		{
			name:    "NoFrontmatter",
			content: "Just text\n",
			errIs:   errNoRuleFrontmatter,
		},
		{
			name:    "MissingClosingDelimiter",
			content: "---\nalwaysApply: true\nBody",
			errText: "missing closing front-matter delimiter",
		},
		{
			name:    "MalformedYAML",
			content: "---\nalwaysApply: [true\n---\nBody",
			errText: "parse front-matter",
		},
		{
			name:    "AlwaysApplyNotBool",
			content: "---\nalwaysApply: sometimes\n---\nBody",
			errText: "parse front-matter",
		},
		{
			name:    "GlobsNotStringOrList",
			content: "---\nglobs:\n  ts: true\n---\nBody",
			errText: "globs must be a string or a list of strings",
		},
		{
			name:    "GlobEntryNotString",
			content: "---\nglobs: [1]\n---\nBody",
			errText: "globs entries must be strings",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, body, err := parseRule(tt.content)
			switch {
			case tt.errIs != nil:
				require.ErrorIs(t, err, tt.errIs)
				return
			case tt.errText != "":
				require.ErrorContains(t, err, tt.errText)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.body, body)
		})
	}
}

func TestIsRulePath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path string
		want bool
	}{
		{path: "/w/.cursor/rules/go.mdc", want: true},
		{path: "/w/.cursor/rules/frontend/components.mdc", want: true},
		{path: "/home/u/.coder/rules/go.mdc", want: true},
		{path: "/w/.cursor/rules/go.md", want: false},
		{path: "/w/.cursor/go.mdc", want: false},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, isRulePath(tt.path), tt.path)
	}
}

func TestDefaultBuiltinRootsIncludeUserRuleDirs(t *testing.T) {
	t.Parallel()
	roots := defaultBuiltinRoots()
	require.Contains(t, roots, "~/.coder/rules")
	require.Contains(t, roots, "~/.cursor/rules")
}
