package plugins

import (
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/x/pluginsdk/pluginproto"
	"github.com/coder/safedial"
)

// MinTickSeconds is the shortest tick interval a plugin can request.
const MinTickSeconds = 2

var (
	pluginNameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	configKeyRegex  = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,63}$`)

	knownEvents = []string{codersdk.PluginEventChatMessagesAdded, codersdk.PluginEventChatStatusChanged}
	knownChats  = []string{codersdk.PluginChatCreate, codersdk.PluginChatSend, codersdk.PluginChatRead}
)

// ManifestFromProto converts the manifest a module returns.
func ManifestFromProto(m *pluginproto.Manifest) codersdk.PluginManifest {
	return codersdk.PluginManifest{
		Name:        m.GetName(),
		Version:     m.GetVersion(),
		Events:      nonNil(m.GetEvents()),
		Chats:       nonNil(m.GetChats()),
		HTTPOrigins: nonNil(m.GetHttpOrigins()),
		TickSeconds: m.GetTickSeconds(),
		Webhook:     m.GetWebhook(),
		ConfigKeys:  nonNil(m.GetConfigKeys()),
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ValidateManifest rejects a manifest that coderd cannot run.
func ValidateManifest(m codersdk.PluginManifest) error {
	if !pluginNameRegex.MatchString(m.Name) {
		return xerrors.Errorf("plugin name %q must be 1 to 63 lowercase letters, digits or dashes", m.Name)
	}
	if len(m.Version) > 64 {
		return xerrors.New("plugin version must be 64 characters at most")
	}
	for _, e := range m.Events {
		if !slices.Contains(knownEvents, e) {
			return xerrors.Errorf("unknown event %q", e)
		}
	}
	for _, c := range m.Chats {
		if !slices.Contains(knownChats, c) {
			return xerrors.Errorf("unknown chat capability %q", c)
		}
	}
	for _, o := range m.HTTPOrigins {
		if _, err := parseOrigin(o); err != nil {
			return err
		}
	}
	if m.TickSeconds != 0 && m.TickSeconds < MinTickSeconds {
		return xerrors.Errorf("tick_seconds must be 0 or at least %d", MinTickSeconds)
	}
	for _, k := range m.ConfigKeys {
		if !configKeyRegex.MatchString(k) {
			return xerrors.Errorf("config key %q must be a letter followed by letters, digits or underscores", k)
		}
	}
	return nil
}

// ValidateGrants makes sure the grants cover exactly what the manifest
// requests, so the owner sees and accepts every capability.
func ValidateGrants(m codersdk.PluginManifest, g codersdk.PluginGrants) error {
	pairs := []struct {
		name            string
		manifest, grant []string
	}{
		{"events", m.Events, g.Events},
		{"chats", m.Chats, g.Chats},
		{"http_origins", m.HTTPOrigins, g.HTTPOrigins},
		{"config_keys", m.ConfigKeys, g.ConfigKeys},
	}
	for _, p := range pairs {
		if !sameSet(p.manifest, p.grant) {
			return xerrors.Errorf("grants for %s must equal the manifest: requested %v, granted %v", p.name, p.manifest, p.grant)
		}
	}
	if m.TickSeconds != g.TickSeconds {
		return xerrors.Errorf("grant for tick_seconds must equal the manifest: requested %d, granted %d", m.TickSeconds, g.TickSeconds)
	}
	if m.Webhook != g.Webhook {
		return xerrors.Errorf("grant for webhook must equal the manifest: requested %t, granted %t", m.Webhook, g.Webhook)
	}
	if _, err := ParsePrivateCIDRs(g.PrivateCIDRs); err != nil {
		return err
	}
	return nil
}

// ParsePrivateCIDRs parses the private ranges an owner granted.
func ParsePrivateCIDRs(cidrs []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		p, err := safedial.ParseAllowedPrefix(c)
		if err != nil {
			return nil, xerrors.Errorf("private CIDR %q: %w", c, err)
		}
		prefixes = append(prefixes, p)
	}
	return prefixes, nil
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(slices.Compact(a), slices.Compact(b))
}

type origin struct {
	scheme string
	host   string
	port   string
}

func parseOrigin(raw string) (origin, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return origin{}, xerrors.Errorf("http origin %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return origin{}, xerrors.Errorf("http origin %q must use http or https", raw)
	}
	if u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return origin{}, xerrors.Errorf("http origin %q must be scheme://host[:port] only", raw)
	}
	return originOf(u), nil
}

func originOf(u *url.URL) origin {
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return origin{scheme: u.Scheme, host: strings.ToLower(u.Hostname()), port: port}
}

// allowsOrigin reports whether a request URL matches a granted origin by
// scheme, host and port.
func allowsOrigin(granted []string, u *url.URL) bool {
	want := originOf(u)
	for _, g := range granted {
		o, err := parseOrigin(g)
		if err == nil && o == want {
			return true
		}
	}
	return false
}
