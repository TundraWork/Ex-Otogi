package help

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

const helpCommandName = "help"

// Module replies with command reference text when it receives a /help command.
type Module struct {
	dispatcher     platform.SinkDispatcher
	commandCatalog core.CommandCatalog
}

// New creates a help module with default configuration.
func New() *Module {
	return &Module{}
}

// Name returns the stable module identifier.
func (m *Module) Name() string {
	return "help"
}

// Spec declares interest in ordinary help command events.
func (m *Module) Spec() core.ModuleSpec {
	return core.ModuleSpec{
		Handlers: []core.ModuleHandler{
			{
				Capability: core.Capability{
					Name:        "help-command-handler",
					Description: "renders registered command help for /help",
					Interest: core.InterestSet{
						Kinds:          []platform.EventKind{platform.EventKindCommandReceived},
						RequireCommand: true,
						CommandNames:   []string{helpCommandName},
						RequireArticle: true,
					},
					RequiredServices: []string{
						platform.ServiceSinkDispatcher,
						core.ServiceCommandCatalog,
					},
				},
				Subscription: core.NewDefaultSubscriptionSpec("help-commands"),
				Handler:      m.handleCommand,
			},
		},
		Commands: []platform.CommandSpec{
			{
				Prefix:      platform.CommandPrefixOrdinary,
				Name:        helpCommandName,
				Description: "show all available commands",
			},
		},
	}
}

// OnRegister resolves dependencies required by this module.
func (m *Module) OnRegister(_ context.Context, runtime core.ModuleRuntime) error {
	dispatcher, err := core.ResolveAs[platform.SinkDispatcher](
		runtime.Services(),
		platform.ServiceSinkDispatcher,
	)
	if err != nil {
		return fmt.Errorf("help resolve outbound dispatcher: %w", err)
	}
	commandCatalog, err := core.ResolveAs[core.CommandCatalog](
		runtime.Services(),
		core.ServiceCommandCatalog,
	)
	if err != nil {
		return fmt.Errorf("help resolve command catalog: %w", err)
	}

	m.dispatcher = dispatcher
	m.commandCatalog = commandCatalog

	return nil
}

// OnStart starts the module lifecycle.
func (m *Module) OnStart(_ context.Context) error {
	return nil
}

// OnShutdown stops the module lifecycle.
func (m *Module) OnShutdown(_ context.Context) error {
	return nil
}

func (m *Module) handleCommand(ctx context.Context, event *platform.Event) error {
	if event == nil || event.Command == nil || event.Article == nil {
		return nil
	}
	if event.Kind != platform.EventKindCommandReceived {
		return nil
	}
	if event.Command.Name != helpCommandName {
		return nil
	}
	if m.dispatcher == nil {
		return fmt.Errorf("help handle command: outbound dispatcher not configured")
	}
	if m.commandCatalog == nil {
		return fmt.Errorf("help handle command: command catalog not configured")
	}

	commands, err := m.commandCatalog.ListCommands(ctx)
	if err != nil {
		return fmt.Errorf("help list commands: %w", err)
	}
	body := renderHelp(commands)

	target, err := platform.OutboundTargetFromEvent(event)
	if err != nil {
		return fmt.Errorf("help derive outbound target: %w", err)
	}
	_, err = m.dispatcher.SendMessage(ctx, platform.SendMessageRequest{
		Target:           target,
		Text:             body,
		Entities:         helpReplyEntities(body),
		ReplyToMessageID: event.Article.ID,
	})
	if err != nil {
		return fmt.Errorf("help send help message: %w", err)
	}

	return nil
}

func renderHelp(commands []core.RegisteredCommand) string {
	if len(commands) == 0 {
		return "Available commands:\n(none)"
	}

	sorted := append([]core.RegisteredCommand(nil), commands...)
	sort.Slice(sorted, func(i, j int) bool {
		left := commandLabel(sorted[i].Command)
		right := commandLabel(sorted[j].Command)
		if left == right {
			return sorted[i].ModuleName < sorted[j].ModuleName
		}
		return left < right
	})

	lines := make([]string, 0, len(sorted)*4+1)
	lines = append(lines, "Available commands:\n")
	for index, command := range sorted {
		if index > 0 {
			lines = append(lines, "")
		}
		label := commandLabel(command.Command)
		description := strings.TrimSpace(command.Command.Description)
		moduleName := strings.TrimSpace(command.ModuleName)
		if moduleName == "" {
			moduleName = "unknown"
		}

		lines = append(lines, label)
		if len(command.Command.Options) != 0 {
			lines = append(lines, fmt.Sprintf("usage: %s", renderCommandOptions(command.Command.Options)))
		}
		if description != "" {
			lines = append(lines, description)
		}
		lines = append(lines, fmt.Sprintf("(%s)", moduleName))
	}

	return strings.Join(lines, "\n")
}

func helpReplyEntities(body string) []platform.TextEntity {
	if body == "" {
		return nil
	}

	return []platform.TextEntity{
		{
			Type:      platform.TextEntityTypeBlockquote,
			Offset:    0,
			Length:    utf8.RuneCountInString(body),
			Collapsed: true,
		},
	}
}

func commandLabel(command platform.CommandSpec) string {
	return fmt.Sprintf("%s%s", command.Prefix, strings.ToLower(strings.TrimSpace(command.Name)))
}

func renderCommandOptions(options []platform.CommandOptionSpec) string {
	sort.Slice(options, func(i, j int) bool {
		return optionSortKey(options[i]) < optionSortKey(options[j])
	})

	descriptors := make([]string, 0, len(options))
	for _, option := range options {
		descriptor := renderCommandOption(option)
		if descriptor != "" {
			descriptors = append(descriptors, descriptor)
		}
	}
	if len(descriptors) == 0 {
		return "(none)"
	}

	return strings.Join(descriptors, ", ")
}

func optionSortKey(option platform.CommandOptionSpec) string {
	return strings.ToLower(strings.TrimSpace(option.Name)) + "|" + strings.ToLower(strings.TrimSpace(option.Alias))
}

func renderCommandOption(option platform.CommandOptionSpec) string {
	name := strings.ToLower(strings.TrimSpace(option.Name))
	alias := strings.ToLower(strings.TrimSpace(option.Alias))

	var descriptor string
	switch {
	case name != "" && alias != "":
		descriptor = fmt.Sprintf("--%s|-%s", name, alias)
	case name != "":
		descriptor = fmt.Sprintf("--%s", name)
	case alias != "":
		descriptor = fmt.Sprintf("-%s", alias)
	default:
		return ""
	}

	if option.HasValue {
		descriptor += " <value>"
	}
	if option.Required {
		descriptor += " (required)"
	}

	return descriptor
}

var (
	_ core.Module          = (*Module)(nil)
	_ core.ModuleRegistrar = (*Module)(nil)
)
