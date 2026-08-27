package otogi

import "fmt"

const (
	maxMessageParts        = 256
	maxMessageAnnotations  = 512
	maxMessageRelations    = 64
	maxEventExtensions     = 1024
	maxHashEntries         = 512
	maxMessageDepth        = 16
	maxTotalExtensionBytes = 512 * 1024
	maxNormalizedMetadata  = 1024 * 1024
	maxInlineMediaBytes    = 1024 * 1024
)

// Message is an immutable bounded directed acyclic rich-content graph.
type Message struct {
	roots      []PartID
	parts      []Part
	index      map[PartID]int
	relations  []Relation
	extensions []Extension
}

// NewMessage validates graph shape and aggregate limits before owning inputs.
func NewMessage(roots []PartID, parts []Part, relations []Relation, extensions []Extension) (Message, error) {
	if len(parts) > maxMessageParts || len(relations) > maxMessageRelations || len(extensions) > 64 {
		return Message{}, fmt.Errorf("message owner limits: %w", ErrCapacity)
	}
	if err := validateAggregates(parts, relations, extensions); err != nil {
		return Message{}, err
	}
	if len(roots) > maxMessageParts {
		return Message{}, fmt.Errorf("message roots: %w", ErrCapacity)
	}
	index := make(map[PartID]int, len(parts))
	for i, part := range parts {
		if part.id.value == "" || !validPartKind(part.kind) {
			return Message{}, fmt.Errorf("message part: %w", ErrInvalid)
		}
		if _, exists := index[part.id]; exists {
			return Message{}, fmt.Errorf("duplicate part: %w", ErrInvalid)
		}
		index[part.id] = i
	}
	rootSet := make(map[PartID]struct{}, len(roots))
	for _, root := range roots {
		if _, exists := index[root]; !exists {
			return Message{}, fmt.Errorf("missing root: %w", ErrInvalid)
		}
		if _, duplicate := rootSet[root]; duplicate {
			return Message{}, fmt.Errorf("duplicate root: %w", ErrInvalid)
		}
		rootSet[root] = struct{}{}
	}
	for _, part := range parts {
		for _, child := range part.children {
			if _, exists := index[child]; !exists {
				return Message{}, fmt.Errorf("missing child: %w", ErrInvalid)
			}
		}
	}
	if err := validateDAG(parts, index); err != nil {
		return Message{}, err
	}
	ownedParts := make([]Part, len(parts))
	copy(ownedParts, parts)
	ownedRelations := make([]Relation, len(relations))
	copy(ownedRelations, relations)
	ownedIndex := make(map[PartID]int, len(index))
	for id, i := range index {
		ownedIndex[id] = i
	}
	return Message{roots: clonePartIDs(roots), parts: ownedParts, index: ownedIndex, relations: ownedRelations, extensions: cloneExtensions(extensions)}, nil
}

// Roots returns ordered root identifiers.
func (v Message) Roots() []PartID { return clonePartIDs(v.roots) }

// Parts returns parts in their original order.
func (v Message) Parts() []Part { out := make([]Part, len(v.parts)); copy(out, v.parts); return out }

// Part looks up a part by message-local identifier.
func (v Message) Part(id PartID) (Part, bool) {
	i, ok := v.index[id]
	if !ok {
		return Part{}, false
	}
	return v.parts[i], true
}

// Relations returns ordered message-owned relations.
func (v Message) Relations() []Relation {
	out := make([]Relation, len(v.relations))
	copy(out, v.relations)
	return out
}

// Extensions returns ordered message-owned extensions.
func (v Message) Extensions() []Extension { return cloneExtensions(v.extensions) }

func validateDAG(parts []Part, index map[PartID]int) error {
	state := make([]uint8, len(parts))
	heights := make([]int, len(parts))
	var height func(int) (int, error)
	height = func(i int) (int, error) {
		if state[i] == 1 {
			return 0, fmt.Errorf("message cycle: %w", ErrInvalid)
		}
		if state[i] == 2 {
			return heights[i], nil
		}
		state[i] = 1
		maxChild := 0
		for _, child := range parts[i].children {
			childHeight, err := height(index[child])
			if err != nil {
				return 0, err
			}
			if childHeight > maxChild {
				maxChild = childHeight
			}
		}
		heights[i] = maxChild + 1
		if heights[i] > maxMessageDepth {
			return 0, fmt.Errorf("message depth: %w", ErrCapacity)
		}
		state[i] = 2
		return heights[i], nil
	}
	for i := range parts {
		if _, err := height(i); err != nil {
			return err
		}
	}
	return nil
}

func validateAggregates(parts []Part, relations []Relation, messageExtensions []Extension) error {
	return validateAggregateInputs(parts, relations, messageExtensions, nil, ProviderLocator{})
}

func validateEventAggregates(message Message, envelope []Extension, locator ProviderLocator) error {
	return validateAggregateInputs(message.parts, message.relations, message.extensions, envelope, locator)
}

func validateAggregateInputs(parts []Part, relations []Relation, messageExtensions, envelope []Extension, locator ProviderLocator) error {
	annotations, hashes, extensionCount := 0, 0, 0
	var extensionBytes, metadata, inlineBytes uint64
	add := func(dst *uint64, n uint64) bool {
		if ^uint64(0)-*dst < n {
			return false
		}
		*dst += n
		return true
	}
	chargeString := func(s string) bool { return add(&metadata, uint64(len(s))) }
	chargeExtensions := func(values []Extension) bool {
		extensionCount += len(values)
		if extensionCount > maxEventExtensions {
			return false
		}
		for _, ext := range values {
			if len(ext.data) > maxExtensionBytes || !add(&extensionBytes, uint64(len(ext.data))) || !add(&metadata, uint64(len(ext.data))) || !chargeString(ext.namespaceURI) || !chargeString(ext.localName) || !chargeString(ext.mediaType) {
				return false
			}
		}
		return add(&metadata, uint64(len(values))*16)
	}
	if len(relations) > 256 || !chargeExtensions(messageExtensions) || !chargeExtensions(envelope) || !add(&metadata, uint64(len(parts)+len(relations)+len(envelope))*16) || !add(&metadata, uint64(len(locator.data))) || !chargeString(locator.namespaceURI) {
		return fmt.Errorf("message aggregate: %w", ErrCapacity)
	}
	for _, part := range parts {
		if !chargeString(part.id.value) || !chargeString(string(part.kind)) || !chargeString(part.text) || !add(&metadata, uint64(len(part.children)+len(part.annotations))*16) || !chargeExtensions(part.extensions) {
			return fmt.Errorf("message aggregate: %w", ErrCapacity)
		}
		annotations += len(part.annotations)
		if annotations > maxMessageAnnotations {
			return fmt.Errorf("message annotations: %w", ErrCapacity)
		}
		for _, annotation := range part.annotations {
			if !chargeString(annotation.id) || !chargeString(string(annotation.kind)) || !chargeString(annotation.target) || !chargeExtensions(annotation.extensions) {
				return fmt.Errorf("message aggregate: %w", ErrCapacity)
			}
		}
		if part.kind == PartMedia || part.kind == PartFile {
			hashes += len(part.media.hashes)
			if hashes > maxHashEntries {
				return fmt.Errorf("message hashes: %w", ErrCapacity)
			}
			if !chargeString(string(part.media.role)) || !chargeString(part.media.mediaType) || !chargeString(part.media.filename) || !add(&metadata, uint64(len(part.media.hashes))*16) || !chargeExtensions(part.media.extensions) || !chargeExtensions(part.media.source.extensions) {
				return fmt.Errorf("message aggregate: %w", ErrCapacity)
			}
			for _, hash := range part.media.hashes {
				if !chargeString(hash.algorithm) || !add(&metadata, uint64(len(hash.value))) {
					return fmt.Errorf("message hash metadata: %w", ErrCapacity)
				}
			}
			if part.media.source.kind == MediaSourceExternal && !chargeString(part.media.source.external) {
				return fmt.Errorf("message source metadata: %w", ErrCapacity)
			}
			if part.media.source.kind == MediaSourceInline && !add(&inlineBytes, uint64(len(part.media.source.inline))) {
				return fmt.Errorf("message inline media: %w", ErrCapacity)
			}
			if part.media.source.kind == MediaSourceOpaque && (!add(&metadata, uint64(len(part.media.source.locator.data))) || !chargeString(part.media.source.locator.namespaceURI)) {
				return fmt.Errorf("message metadata: %w", ErrCapacity)
			}
		}
		if typedPartKind(part.kind) || part.kind == PartOpaque {
			if !chargeExtensions([]Extension{part.value}) {
				return fmt.Errorf("message value: %w", ErrCapacity)
			}
		}
	}
	for _, relation := range relations {
		if !chargeString(string(relation.kind)) || !chargeString(relation.key) || !chargeExtensions(relation.extensions) || !chargeString(relation.target.platform.value) || !chargeString(relation.target.account.value) || !chargeString(relation.target.conversation.value) || !chargeString(relation.target.sourceEvent.value) || !chargeString(relation.target.locator.namespaceURI) || !add(&metadata, uint64(len(relation.target.locator.data))) {
			return fmt.Errorf("message relation aggregate: %w", ErrCapacity)
		}
	}
	if extensionBytes > maxTotalExtensionBytes || metadata > maxNormalizedMetadata || inlineBytes > maxInlineMediaBytes {
		return fmt.Errorf("message aggregate: %w", ErrCapacity)
	}
	return nil
}

func validPartKind(kind PartKind) bool {
	return kind == PartText || kind == PartMedia || kind == PartFile || typedPartKind(kind) || kind == PartOpaque
}
