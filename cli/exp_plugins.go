package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/cli/cliui"
	"github.com/coder/coder/v2/coderd/util/ptr"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/pretty"
	"github.com/coder/serpent"
)

func (r *RootCmd) pluginsCommand() *serpent.Command {
	return &serpent.Command{
		Use:   "plugins",
		Short: "Manage sandboxed Wasm plugins for Coder Agents chats",
		Long: "Plugins are Wasm modules that bridge Coder Agents chats to services such as Signal or Telegram. " +
			"Each plugin runs in a sandbox and gets only the capabilities that an owner grants. " +
			"Requires the \"plugins\" experiment.\n" +
			FormatExamples(
				Example{
					Description: "Install a plugin and let it reach a private signal-cli-rest-api",
					Command:     "coder exp plugins install signal-bridge.wasm --allow-private-cidr 10.0.4.0/24 --config number=+15551234567",
				},
				Example{
					Description: "List the plugins of the current organization",
					Command:     "coder exp plugins list",
				},
			),
		Handler: func(inv *serpent.Invocation) error {
			return inv.Command.HelpHandler(inv)
		},
		Children: []*serpent.Command{
			r.pluginsInstall(),
			r.pluginsList(),
			r.pluginsSetEnabled("enable", "Start a plugin again", ptr.Ref(true)),
			r.pluginsSetEnabled("disable", "Stop a plugin and keep its links", ptr.Ref(false)),
			r.pluginsConfig(),
			r.pluginsDelete(),
		},
	}
}

func (r *RootCmd) pluginsInstall() *serpent.Command {
	var (
		orgContext   = NewOrganizationContext()
		privateCIDRs []string
		config       []string
	)
	cmd := &serpent.Command{
		Use:        "install <file.wasm>",
		Short:      "Install a plugin after you accept the capabilities it requests",
		Middleware: serpent.RequireNArgs(1),
		Options: serpent.OptionSet{
			{
				Flag:        "allow-private-cidr",
				Description: "Let HTTP requests of the plugin reach this private address range. Repeat for more ranges.",
				Value:       serpent.StringArrayOf(&privateCIDRs),
			},
			{
				Flag:        "config",
				Description: "Set a config value as key=value. Repeat for more keys. The plugin manifest lists the keys.",
				Value:       serpent.StringArrayOf(&config),
			},
			cliui.SkipPromptOption(),
		},
		Handler: func(inv *serpent.Invocation) error {
			client, err := r.InitClient(inv)
			if err != nil {
				return err
			}
			org, err := orgContext.Selected(inv, client)
			if err != nil {
				return err
			}
			values, err := parsePluginConfig(config)
			if err != nil {
				return err
			}
			wasm, err := os.ReadFile(inv.Args[0])
			if err != nil {
				return xerrors.Errorf("read module: %w", err)
			}
			exp := codersdk.NewExperimentalClient(client)
			inspected, err := exp.InspectPlugin(inv.Context(), org.ID, wasm)
			if err != nil {
				return xerrors.Errorf("inspect module: %w", err)
			}
			_, _ = fmt.Fprint(inv.Stdout, describePluginRequest(inspected, privateCIDRs))
			_, err = cliui.Prompt(inv, cliui.PromptOptions{
				Text:      "Grant all?",
				IsConfirm: true,
				Default:   cliui.ConfirmNo,
			})
			if err != nil {
				return err
			}
			plugin, err := exp.CreatePlugin(inv.Context(), org.ID, codersdk.CreatePluginRequest{
				Wasm:   wasm,
				Grants: codersdk.GrantsForManifest(inspected.Manifest, privateCIDRs),
				Config: values,
			})
			if err != nil {
				return xerrors.Errorf("install plugin: %w", err)
			}
			_, _ = fmt.Fprintf(inv.Stdout, "%s Installed %s in %s. ID: %s\n",
				pretty.Sprint(cliui.DefaultStyles.Keyword, "✓"), plugin.Name, org.Name, plugin.ID)
			return nil
		},
	}
	orgContext.AttachOptions(cmd)
	return cmd
}

func describePluginRequest(inspected codersdk.InspectPluginResponse, privateCIDRs []string) string {
	m := inspected.Manifest
	var b strings.Builder
	short := inspected.SHA256
	if len(short) > 12 {
		short = short[:12]
	}
	_, _ = fmt.Fprintf(&b, "%s v%s  sha256:%s\n", m.Name, m.Version, short)
	_, _ = fmt.Fprintln(&b, "The plugin requests:")
	line := func(name string, values []string, suffix string) {
		if len(values) == 0 {
			return
		}
		_, _ = fmt.Fprintf(&b, "  %-8s %s%s\n", name, strings.Join(values, ", "), suffix)
	}
	line("events", m.Events, "")
	line("chats", m.Chats, " (as linked users)")
	line("http", m.HTTPOrigins, "")
	if m.TickSeconds > 0 {
		_, _ = fmt.Fprintf(&b, "  %-8s every %ds\n", "tick", m.TickSeconds)
	}
	if m.Webhook {
		_, _ = fmt.Fprintf(&b, "  %-8s POST /api/experimental/plugins/{id}/webhook, no authentication\n", "webhook")
	}
	line("config", m.ConfigKeys, "")
	line("private", privateCIDRs, " (from --allow-private-cidr)")
	return b.String()
}

func parsePluginConfig(pairs []string) (map[string]string, error) {
	values := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, xerrors.Errorf("config %q must be key=value", p)
		}
		values[k] = v
	}
	return values, nil
}

type pluginRow struct {
	ID       uuid.UUID `table:"id"`
	Name     string    `table:"name,default_sort"`
	Version  string    `table:"version"`
	Enabled  bool      `table:"enabled"`
	Running  string    `table:"running on"`
	LastErr  string    `table:"last error"`
	Chats    string    `table:"chats"`
	Origins  string    `table:"http origins"`
	ConfigOK string    `table:"config set"`
}

func (r *RootCmd) pluginsList() *serpent.Command {
	orgContext := NewOrganizationContext()
	formatter := cliui.NewOutputFormatter(
		cliui.ChangeFormatterData(
			cliui.TableFormat([]pluginRow{}, []string{"name", "version", "enabled", "running on", "last error"}),
			func(data any) (any, error) {
				plugins, ok := data.([]codersdk.Plugin)
				if !ok {
					return nil, xerrors.Errorf("expected []codersdk.Plugin, got %T", data)
				}
				rows := make([]pluginRow, 0, len(plugins))
				for _, p := range plugins {
					running := ""
					if p.RunnerID != nil {
						running = p.RunnerID.String()
					}
					rows = append(rows, pluginRow{
						ID:       p.ID,
						Name:     p.Name,
						Version:  p.Version,
						Enabled:  p.Enabled,
						Running:  running,
						LastErr:  p.LastError,
						Chats:    strings.Join(p.Grants.Chats, ", "),
						Origins:  strings.Join(p.Grants.HTTPOrigins, ", "),
						ConfigOK: strings.Join(p.ConfigKeysSet, ", "),
					})
				}
				return rows, nil
			},
		),
		cliui.JSONFormat(),
	)
	cmd := &serpent.Command{
		Use:        "list",
		Short:      "List the plugins of an organization",
		Middleware: serpent.RequireNArgs(0),
		Handler: func(inv *serpent.Invocation) error {
			client, err := r.InitClient(inv)
			if err != nil {
				return err
			}
			org, err := orgContext.Selected(inv, client)
			if err != nil {
				return err
			}
			plugins, err := codersdk.NewExperimentalClient(client).Plugins(inv.Context(), org.ID)
			if err != nil {
				return xerrors.Errorf("list plugins: %w", err)
			}
			out, err := formatter.Format(inv.Context(), plugins)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(inv.Stdout, out)
			return err
		},
	}
	orgContext.AttachOptions(cmd)
	formatter.AttachOptions(&cmd.Options)
	return cmd
}

// pluginByNameOrID finds a plugin of the selected organization.
func pluginByNameOrID(inv *serpent.Invocation, client *codersdk.Client, org codersdk.Organization, nameOrID string) (codersdk.Plugin, error) {
	exp := codersdk.NewExperimentalClient(client)
	if id, err := uuid.Parse(nameOrID); err == nil {
		return exp.Plugin(inv.Context(), id)
	}
	plugins, err := exp.Plugins(inv.Context(), org.ID)
	if err != nil {
		return codersdk.Plugin{}, xerrors.Errorf("list plugins: %w", err)
	}
	for _, p := range plugins {
		if p.Name == nameOrID {
			return p, nil
		}
	}
	return codersdk.Plugin{}, xerrors.Errorf("no plugin %q in organization %s", nameOrID, org.Name)
}

func (r *RootCmd) pluginsSetEnabled(use, short string, enabled *bool) *serpent.Command {
	orgContext := NewOrganizationContext()
	cmd := &serpent.Command{
		Use:        use + " <name|id>",
		Short:      short,
		Middleware: serpent.RequireNArgs(1),
		Handler: func(inv *serpent.Invocation) error {
			client, err := r.InitClient(inv)
			if err != nil {
				return err
			}
			org, err := orgContext.Selected(inv, client)
			if err != nil {
				return err
			}
			p, err := pluginByNameOrID(inv, client, org, inv.Args[0])
			if err != nil {
				return err
			}
			_, err = codersdk.NewExperimentalClient(client).UpdatePlugin(inv.Context(), p.ID, codersdk.UpdatePluginRequest{Enabled: enabled})
			if err != nil {
				return xerrors.Errorf("update plugin: %w", err)
			}
			_, _ = fmt.Fprintf(inv.Stdout, "Plugin %s is %sd.\n", p.Name, use)
			return nil
		},
	}
	orgContext.AttachOptions(cmd)
	return cmd
}

func (r *RootCmd) pluginsConfig() *serpent.Command {
	var (
		orgContext = NewOrganizationContext()
		config     []string
	)
	cmd := &serpent.Command{
		Use:        "config <name|id>",
		Short:      "Replace all config values of a plugin",
		Middleware: serpent.RequireNArgs(1),
		Options: serpent.OptionSet{
			{
				Flag:        "set",
				Description: "A config value as key=value. Repeat for more keys. Keys you do not set are removed.",
				Value:       serpent.StringArrayOf(&config),
			},
		},
		Handler: func(inv *serpent.Invocation) error {
			client, err := r.InitClient(inv)
			if err != nil {
				return err
			}
			org, err := orgContext.Selected(inv, client)
			if err != nil {
				return err
			}
			values, err := parsePluginConfig(config)
			if err != nil {
				return err
			}
			p, err := pluginByNameOrID(inv, client, org, inv.Args[0])
			if err != nil {
				return err
			}
			_, err = codersdk.NewExperimentalClient(client).UpdatePlugin(inv.Context(), p.ID, codersdk.UpdatePluginRequest{Config: &values})
			if err != nil {
				return xerrors.Errorf("update plugin config: %w", err)
			}
			_, _ = fmt.Fprintf(inv.Stdout, "Updated the config of %s. The plugin restarts.\n", p.Name)
			return nil
		},
	}
	orgContext.AttachOptions(cmd)
	return cmd
}

func (r *RootCmd) pluginsDelete() *serpent.Command {
	orgContext := NewOrganizationContext()
	cmd := &serpent.Command{
		Use:        "delete <name|id>",
		Short:      "Remove a plugin with its links and chat bindings",
		Middleware: serpent.RequireNArgs(1),
		Options:    serpent.OptionSet{cliui.SkipPromptOption()},
		Handler: func(inv *serpent.Invocation) error {
			client, err := r.InitClient(inv)
			if err != nil {
				return err
			}
			org, err := orgContext.Selected(inv, client)
			if err != nil {
				return err
			}
			p, err := pluginByNameOrID(inv, client, org, inv.Args[0])
			if err != nil {
				return err
			}
			_, err = cliui.Prompt(inv, cliui.PromptOptions{
				Text:      fmt.Sprintf("Delete plugin %s and all of its links?", p.Name),
				IsConfirm: true,
				Default:   cliui.ConfirmNo,
			})
			if err != nil {
				return err
			}
			if err := codersdk.NewExperimentalClient(client).DeletePlugin(inv.Context(), p.ID); err != nil {
				return xerrors.Errorf("delete plugin: %w", err)
			}
			_, _ = fmt.Fprintf(inv.Stdout, "Deleted plugin %s.\n", p.Name)
			return nil
		},
	}
	orgContext.AttachOptions(cmd)
	return cmd
}
