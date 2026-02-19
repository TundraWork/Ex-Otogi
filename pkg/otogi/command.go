package otogi

import (
	"fmt"
	"strings"
)

// CommandPrefix identifies the prefix introducing one command invocation.
type CommandPrefix string

const (
	// CommandPrefixOrdinary identifies ordinary command syntax.
	CommandPrefixOrdinary CommandPrefix = "/"
	// CommandPrefixSystem identifies system command syntax.
	CommandPrefixSystem CommandPrefix = "~"
)

// Validate checks whether one command prefix is supported.
func (p CommandPrefix) Validate() error {
	switch p {
	case CommandPrefixOrdinary, CommandPrefixSystem:
		return nil
	default:
		return fmt.Errorf("validate command prefix: unsupported prefix %q", p)
	}
}

// CommandScope identifies the semantic scope implied by a command prefix.
type CommandScope string

const (
	// CommandScopeOrdinary identifies ordinary user-facing commands.
	CommandScopeOrdinary CommandScope = "ordinary"
	// CommandScopeSystem identifies system-level commands.
	CommandScopeSystem CommandScope = "system"
)

// Validate checks whether one command scope is supported.
func (s CommandScope) Validate() error {
	switch s {
	case CommandScopeOrdinary, CommandScopeSystem:
		return nil
	default:
		return fmt.Errorf("validate command scope: unsupported scope %q", s)
	}
}

// ScopeFromPrefix maps a command prefix to its semantic scope.
func ScopeFromPrefix(prefix CommandPrefix) (CommandScope, error) {
	switch prefix {
	case CommandPrefixOrdinary:
		return CommandScopeOrdinary, nil
	case CommandPrefixSystem:
		return CommandScopeSystem, nil
	default:
		return "", fmt.Errorf("scope from prefix: unsupported prefix %q", prefix)
	}
}

// CommandCandidate is a parsed command-looking message before command-spec binding.
type CommandCandidate struct {
	// Prefix is the leading command prefix.
	Prefix CommandPrefix
	// Scope is the semantic scope derived from Prefix.
	Scope CommandScope
	// Name is the normalized command name without prefix and mention suffix.
	Name string
	// Mention is the optional mention suffix from `<name>@<mention>`.
	Mention string
	// RawInput is the original untrimmed article text.
	RawInput string
	// Tokens stores command tail tokens after the command header token.
	Tokens []string
}

// CommandOption is one parsed command option in a bound invocation.
type CommandOption struct {
	// Name is the normalized long option name.
	Name string
	// Alias is the normalized short option alias when declared.
	Alias string
	// Value is the consumed option value when HasValue is true.
	Value string
	// HasValue reports whether this option consumed one value token.
	HasValue bool
}

// CommandInvocation carries one validated command event payload.
type CommandInvocation struct {
	// Name is the normalized command name.
	Name string
	// Mention is the optional mention suffix from `<name>@<mention>`.
	Mention string
	// Value stores the remaining non-option tail text joined by spaces.
	Value string
	// Options stores parsed options defined by the bound command spec.
	Options []CommandOption
	// SourceEventID identifies the inbound source event that produced this command.
	SourceEventID string
	// SourceEventKind identifies the inbound source event kind.
	SourceEventKind EventKind
	// RawInput stores the original inbound article text.
	RawInput string
}

// Validate checks command invocation contract fields.
func (c *CommandInvocation) Validate() error {
	if c == nil {
		return fmt.Errorf("validate command invocation: nil invocation")
	}
	if normalizeCommandName(c.Name) == "" {
		return fmt.Errorf("validate command invocation: missing name")
	}
	if c.SourceEventID == "" {
		return fmt.Errorf("validate command invocation: missing source_event_id")
	}
	if c.SourceEventKind == "" {
		return fmt.Errorf("validate command invocation: missing source_event_kind")
	}

	return nil
}

// CommandOptionSpec declares one available option in one command registration.
type CommandOptionSpec struct {
	// Name is the long option key used as `--<name>`.
	Name string
	// Alias is the short option key used as `-<alias>`.
	Alias string
	// HasValue reports whether the option must consume one following value token.
	HasValue bool
	// Required reports whether this option must appear in one invocation.
	Required bool
	// Description describes option behavior for diagnostics and help text.
	Description string
}

// Validate checks command option specification coherence.
func (s CommandOptionSpec) Validate() error {
	name := normalizeCommandName(s.Name)
	alias := normalizeCommandAlias(s.Alias)
	if name == "" && alias == "" {
		return fmt.Errorf("validate command option spec: missing name and alias")
	}
	if alias != "" && len(alias) != 1 {
		return fmt.Errorf("validate command option spec: alias %q must be one character", s.Alias)
	}
	if strings.ContainsAny(name, " \t\r\n") {
		return fmt.Errorf("validate command option spec: name %q contains whitespace", s.Name)
	}
	if strings.ContainsAny(alias, " \t\r\n") {
		return fmt.Errorf("validate command option spec: alias %q contains whitespace", s.Alias)
	}

	return nil
}

// CommandSpec declares one module command registration.
type CommandSpec struct {
	// Prefix identifies which command prefix triggers this command.
	Prefix CommandPrefix
	// Name is the command name without prefix and mention suffix.
	Name string
	// Description describes command behavior for diagnostics and help text.
	Description string
	// Options declares supported command options.
	Options []CommandOptionSpec
}

// Validate checks command specification coherence.
func (s CommandSpec) Validate() error {
	if err := s.Prefix.Validate(); err != nil {
		return fmt.Errorf("validate command spec %q: %w", s.Name, err)
	}
	if normalizeCommandName(s.Name) == "" {
		return fmt.Errorf("validate command spec: missing name")
	}

	seenNames := make(map[string]struct{}, len(s.Options))
	seenAliases := make(map[string]struct{}, len(s.Options))
	for index, option := range s.Options {
		if err := option.Validate(); err != nil {
			return fmt.Errorf("validate command spec %s option[%d]: %w", s.Name, index, err)
		}

		name := normalizeCommandName(option.Name)
		if name != "" {
			if _, exists := seenNames[name]; exists {
				return fmt.Errorf("validate command spec %s: duplicate option name %q", s.Name, option.Name)
			}
			seenNames[name] = struct{}{}
		}

		alias := normalizeCommandAlias(option.Alias)
		if alias != "" {
			if _, exists := seenAliases[alias]; exists {
				return fmt.Errorf("validate command spec %s: duplicate option alias %q", s.Name, option.Alias)
			}
			seenAliases[alias] = struct{}{}
		}
	}

	return nil
}

// ParseCommandCandidate parses one input text into a command candidate.
//
// matched is false when text does not look like a command. When matched is true,
// candidate fields are populated as much as possible and err reports syntax
// issues such as missing command name or unsupported option token format.
func ParseCommandCandidate(text string) (candidate CommandCandidate, matched bool, err error) {
	candidate.RawInput = text

	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return candidate, false, nil
	}
	header := fields[0]
	if header == "" {
		return candidate, false, nil
	}

	prefix, matched := parseCommandPrefix(header)
	if !matched {
		return candidate, false, nil
	}
	candidate.Prefix = prefix

	scope, scopeErr := ScopeFromPrefix(prefix)
	if scopeErr != nil {
		return candidate, true, scopeErr
	}
	candidate.Scope = scope

	headerBody := header[1:]
	name, mention := splitCommandHeader(headerBody)
	candidate.Name = normalizeCommandName(name)
	candidate.Mention = strings.TrimSpace(mention)
	if candidate.Name == "" {
		return candidate, true, fmt.Errorf("parse command candidate: missing command name")
	}

	if len(fields) > 1 {
		candidate.Tokens = append([]string(nil), fields[1:]...)
	}
	for _, token := range candidate.Tokens {
		if strings.HasPrefix(token, "--") && strings.Contains(token, "=") {
			return candidate, true, fmt.Errorf("parse command candidate: unsupported option format %q", token)
		}
	}

	return candidate, true, nil
}

// BindCommand validates one parsed candidate against one command spec.
//
// sourceEvent must identify the inbound event that produced this command.
func BindCommand(
	candidate CommandCandidate,
	spec CommandSpec,
	sourceEvent *Event,
) (CommandInvocation, error) {
	if sourceEvent == nil {
		return CommandInvocation{}, fmt.Errorf("bind command: nil source event")
	}
	if err := spec.Validate(); err != nil {
		return CommandInvocation{}, fmt.Errorf("bind command %s: %w", spec.Name, err)
	}
	if candidate.Prefix != spec.Prefix {
		return CommandInvocation{}, fmt.Errorf(
			"bind command %s: prefix mismatch, got %q want %q",
			spec.Name,
			candidate.Prefix,
			spec.Prefix,
		)
	}

	specName := normalizeCommandName(spec.Name)
	if normalizeCommandName(candidate.Name) != specName {
		return CommandInvocation{}, fmt.Errorf(
			"bind command %s: name mismatch, got %q",
			spec.Name,
			candidate.Name,
		)
	}

	byName := make(map[string]CommandOptionSpec, len(spec.Options))
	byAlias := make(map[string]CommandOptionSpec, len(spec.Options))
	for _, option := range spec.Options {
		name := normalizeCommandName(option.Name)
		if name != "" {
			byName[name] = option
		}
		alias := normalizeCommandAlias(option.Alias)
		if alias != "" {
			byAlias[alias] = option
		}
	}

	options := make([]CommandOption, 0, len(candidate.Tokens))
	valueTokens := make([]string, 0, len(candidate.Tokens))
	seenRequired := make(map[string]struct{}, len(spec.Options))

	for index := 0; index < len(candidate.Tokens); {
		token := candidate.Tokens[index]

		longName, isLong := parseLongOptionToken(token)
		if isLong {
			optionSpec, exists := byName[longName]
			if !exists {
				return CommandInvocation{}, fmt.Errorf("bind command %s: unknown option --%s", spec.Name, longName)
			}

			option := CommandOption{
				Name:  normalizeCommandName(optionSpec.Name),
				Alias: normalizeCommandAlias(optionSpec.Alias),
			}
			if optionSpec.HasValue {
				if index+1 >= len(candidate.Tokens) {
					return CommandInvocation{}, fmt.Errorf(
						"bind command %s: option --%s requires a value",
						spec.Name,
						longName,
					)
				}
				if looksLikeOptionToken(candidate.Tokens[index+1]) {
					return CommandInvocation{}, fmt.Errorf(
						"bind command %s: option --%s requires a value",
						spec.Name,
						longName,
					)
				}
				option.HasValue = true
				option.Value = candidate.Tokens[index+1]
				index += 2
			} else {
				index++
			}
			options = append(options, option)
			if optionSpec.Required {
				seenRequired[requiredOptionKey(optionSpec)] = struct{}{}
			}
			continue
		}

		shortAlias, isShort := parseShortOptionToken(token)
		if isShort {
			optionSpec, exists := byAlias[shortAlias]
			if !exists {
				return CommandInvocation{}, fmt.Errorf("bind command %s: unknown option -%s", spec.Name, shortAlias)
			}

			option := CommandOption{
				Name:  normalizeCommandName(optionSpec.Name),
				Alias: normalizeCommandAlias(optionSpec.Alias),
			}
			if optionSpec.HasValue {
				if index+1 >= len(candidate.Tokens) {
					return CommandInvocation{}, fmt.Errorf(
						"bind command %s: option -%s requires a value",
						spec.Name,
						shortAlias,
					)
				}
				if looksLikeOptionToken(candidate.Tokens[index+1]) {
					return CommandInvocation{}, fmt.Errorf(
						"bind command %s: option -%s requires a value",
						spec.Name,
						shortAlias,
					)
				}
				option.HasValue = true
				option.Value = candidate.Tokens[index+1]
				index += 2
			} else {
				index++
			}
			options = append(options, option)
			if optionSpec.Required {
				seenRequired[requiredOptionKey(optionSpec)] = struct{}{}
			}
			continue
		}

		valueTokens = append(valueTokens, token)
		index++
	}

	for _, option := range spec.Options {
		if !option.Required {
			continue
		}
		if _, exists := seenRequired[requiredOptionKey(option)]; exists {
			continue
		}

		return CommandInvocation{}, fmt.Errorf(
			"bind command %s: missing required option %s",
			spec.Name,
			commandOptionUsage(option),
		)
	}

	invocation := CommandInvocation{
		Name:            specName,
		Mention:         candidate.Mention,
		Value:           strings.Join(valueTokens, " "),
		Options:         options,
		SourceEventID:   sourceEvent.ID,
		SourceEventKind: sourceEvent.Kind,
		RawInput:        candidate.RawInput,
	}
	if err := invocation.Validate(); err != nil {
		return CommandInvocation{}, fmt.Errorf("bind command %s: %w", spec.Name, err)
	}

	return invocation, nil
}

func parseCommandPrefix(token string) (CommandPrefix, bool) {
	switch {
	case strings.HasPrefix(token, string(CommandPrefixOrdinary)):
		return CommandPrefixOrdinary, true
	case strings.HasPrefix(token, string(CommandPrefixSystem)):
		return CommandPrefixSystem, true
	default:
		return "", false
	}
}

func splitCommandHeader(token string) (name string, mention string) {
	if token == "" {
		return "", ""
	}
	separator := strings.Index(token, "@")
	if separator < 0 {
		return token, ""
	}

	return token[:separator], token[separator+1:]
}

func parseLongOptionToken(token string) (name string, ok bool) {
	if !strings.HasPrefix(token, "--") || len(token) <= 2 {
		return "", false
	}
	if strings.Contains(token, "=") {
		return "", false
	}

	return normalizeCommandName(token[2:]), true
}

func parseShortOptionToken(token string) (alias string, ok bool) {
	if len(token) != 2 {
		return "", false
	}
	if !strings.HasPrefix(token, "-") || strings.HasPrefix(token, "--") {
		return "", false
	}

	return normalizeCommandAlias(token[1:]), true
}

func looksLikeOptionToken(token string) bool {
	if longName, ok := parseLongOptionToken(token); ok && longName != "" {
		return true
	}
	if shortName, ok := parseShortOptionToken(token); ok && shortName != "" {
		return true
	}

	return false
}

func requiredOptionKey(option CommandOptionSpec) string {
	name := normalizeCommandName(option.Name)
	if name != "" {
		return "name:" + name
	}

	return "alias:" + normalizeCommandAlias(option.Alias)
}

func commandOptionUsage(option CommandOptionSpec) string {
	name := normalizeCommandName(option.Name)
	if name != "" {
		return "--" + name
	}

	return "-" + normalizeCommandAlias(option.Alias)
}

func normalizeCommandName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeCommandAlias(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
