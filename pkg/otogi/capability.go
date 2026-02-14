package otogi

// Capability describes what a module can process and what resources it requires.
type Capability struct {
	// Name is a stable capability identifier within the module.
	Name string
	// Description explains the capability intent for operators and tooling.
	Description string
	// Interest declares which neutral events the capability can process.
	Interest InterestSet
	// RequiredServices lists service registry keys required before activation.
	RequiredServices []string
	// Metadata stores optional extension attributes used by runtime tooling.
	Metadata map[string]string
}

// InterestSet describes event selection criteria for capability negotiation.
type InterestSet struct {
	// Kinds restricts matching to specific event kinds when non-empty.
	Kinds []EventKind
	// MediaTypes restricts matching to events carrying at least one listed media type.
	MediaTypes []MediaType
	// RequireMutation requires mutation payload presence.
	RequireMutation bool
	// RequireReaction requires reaction payload presence.
	RequireReaction bool
	// RequireStateChange requires state-change payload presence.
	RequireStateChange bool
}

// Matches reports whether an event satisfies the declared interest set.
func (i InterestSet) Matches(event *Event) bool {
	if event == nil {
		return false
	}
	if len(i.Kinds) > 0 && !containsKind(i.Kinds, event.Kind) {
		return false
	}
	if i.RequireMutation && event.Mutation == nil {
		return false
	}
	if i.RequireReaction && event.Reaction == nil {
		return false
	}
	if i.RequireStateChange && event.StateChange == nil {
		return false
	}
	if len(i.MediaTypes) > 0 && !eventContainsMediaType(event, i.MediaTypes) {
		return false
	}

	return true
}

// Allows reports whether this interest set can safely satisfy another filter.
func (i InterestSet) Allows(filter InterestSet) bool {
	if len(i.Kinds) > 0 && !allKindsIncluded(filter.Kinds, i.Kinds) {
		return false
	}
	if len(i.MediaTypes) > 0 && !allMediaTypesIncluded(filter.MediaTypes, i.MediaTypes) {
		return false
	}
	if i.RequireMutation && !filter.RequireMutation {
		return false
	}
	if i.RequireReaction && !filter.RequireReaction {
		return false
	}
	if i.RequireStateChange && !filter.RequireStateChange {
		return false
	}

	return true
}

// containsKind reports whether target is present in kinds.
func containsKind(kinds []EventKind, target EventKind) bool {
	for _, candidate := range kinds {
		if candidate == target {
			return true
		}
	}

	return false
}

// eventContainsMediaType checks effective event media across message and mutation payloads.
func eventContainsMediaType(event *Event, types []MediaType) bool {
	for _, media := range event.MessageMedia() {
		if containsMediaType(types, media.Type) {
			return true
		}
	}

	return false
}

// messageMedia returns the canonical media payload for filtering purposes.
// For mutation events it prefers the post-mutation snapshot.
func (e *Event) messageMedia() []MediaAttachment {
	if e == nil {
		return nil
	}
	if e.Message != nil {
		return e.Message.Media
	}
	if e.Mutation != nil && e.Mutation.After != nil {
		return e.Mutation.After.Media
	}

	return nil
}

// MessageMedia returns the media payload that best represents this event.
func (e *Event) MessageMedia() []MediaAttachment {
	return e.messageMedia()
}

// allKindsIncluded reports whether subset is fully contained in allowed.
func allKindsIncluded(subset, allowed []EventKind) bool {
	for _, item := range subset {
		if !containsKind(allowed, item) {
			return false
		}
	}

	return true
}

// allMediaTypesIncluded reports whether subset is fully contained in allowed.
func allMediaTypesIncluded(subset, allowed []MediaType) bool {
	for _, item := range subset {
		if !containsMediaType(allowed, item) {
			return false
		}
	}

	return true
}

// containsMediaType reports whether target is present in types.
func containsMediaType(types []MediaType, target MediaType) bool {
	for _, candidate := range types {
		if candidate == target {
			return true
		}
	}

	return false
}
