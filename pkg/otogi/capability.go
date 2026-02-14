package otogi

// Capability describes what a module can process and what resources it requires.
type Capability struct {
	Name             string
	Description      string
	Interest         InterestSet
	RequiredServices []string
	Metadata         map[string]string
}

// InterestSet describes event selection criteria for capability negotiation.
type InterestSet struct {
	Kinds              []EventKind
	MediaTypes         []MediaType
	RequireMutation    bool
	RequireReaction    bool
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
