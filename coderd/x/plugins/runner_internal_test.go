package plugins

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/x/pluginsdk/pluginproto"
)

func TestConnectPromptFromProto(t *testing.T) {
	t.Parallel()
	png := append(append([]byte(nil), pngSignature...), "image data"...)

	for name, tc := range map[string]struct {
		in   *pluginproto.ConnectPrompt
		want codersdk.PluginConnectPrompt
		err  string
	}{
		"QRCodeAndInstructions": {
			in:   &pluginproto.ConnectPrompt{QrCodePng: png, Instructions: "Scan this."},
			want: codersdk.PluginConnectPrompt{QRCodePNG: png, Instructions: "Scan this."},
		},
		"InstructionsOnly": {
			in:   &pluginproto.ConnectPrompt{Instructions: "Send the code."},
			want: codersdk.PluginConnectPrompt{Instructions: "Send the code."},
		},
		"PluginError": {
			in:  &pluginproto.ConnectPrompt{Error: "signal-cli is down", QrCodePng: png},
			err: "signal-cli is down",
		},
		"Empty": {
			in:  &pluginproto.ConnectPrompt{},
			err: "empty connect prompt",
		},
		"NotPNG": {
			in:  &pluginproto.ConnectPrompt{QrCodePng: []byte("<svg/>")},
			err: "must be a PNG image",
		},
		"QRCodeTooLarge": {
			in:  &pluginproto.ConnectPrompt{QrCodePng: append(append([]byte(nil), pngSignature...), bytes.Repeat([]byte{0}, maxQRCodeBytes)...)},
			err: "must be a PNG image",
		},
		"InstructionsTooLong": {
			in:  &pluginproto.ConnectPrompt{Instructions: strings.Repeat("a", maxInstructionsBytes+1)},
			err: "instructions of the plugin must be at most",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := connectPromptFromProto(tc.in)
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
