package otogi

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestPublicAPIHasOnlyLeanContracts(t *testing.T) {
	got, err := exportedPackageNames()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Fields(`
		AccountID AccountID.String ActionMessageCreated ActionMessageDeleted ActionMessageEdited ActionMessageService ActorID ActorID.String
		Annotation Annotation.Extensions Annotation.ID Annotation.Kind Annotation.Length Annotation.Offset Annotation.Target AnnotationBlockQuote AnnotationBold AnnotationCode AnnotationCommand AnnotationEmail AnnotationEmoji AnnotationHashtag AnnotationItalic AnnotationKind AnnotationLink AnnotationMention AnnotationPreformatted AnnotationSpoiler AnnotationStrike AnnotationTimestamp AnnotationUnderline
		CausalEventID CausalEventID.String Completeness CompletenessComplete CompletenessPartial CompletenessRedacted CompletenessUnavailable Contact Contact.Address Contact.DisplayName ContactParams ContactParams.Address ContactParams.DisplayName ConversationID ConversationID.String Direction DirectionIncoming DirectionOutgoing DirectionSystem EmbeddedMessage EmbeddedMessage.Reference EmbeddedMessage.Summary EmbeddedMessageParams EmbeddedMessageParams.Reference EmbeddedMessageParams.Summary
		ErrCapacity ErrInvalid ErrInvalidState ErrModulePanic ErrUnavailable ErrUnsupported Event EventAction EventAction.String EventID EventID.String EventIDGenerator EventIDGenerator.Next
		EventInput EventInput.Account EventInput.Action EventInput.Actor EventInput.CausalEventID EventInput.Completeness EventInput.Conversation EventInput.Direction EventInput.EditedAt EventInput.Extensions EventInput.IngestedAt EventInput.Message EventInput.OccurredAt EventInput.Platform EventInput.ProviderLocator EventInput.SourceEventID EventInput.Tenant EventInput.TraceID
		EventInputParams EventInputParams.Account EventInputParams.Action EventInputParams.Actor EventInputParams.CausalEventID EventInputParams.Completeness EventInputParams.Conversation EventInputParams.Direction EventInputParams.EditedAt EventInputParams.Extensions EventInputParams.IngestedAt EventInputParams.Message EventInputParams.OccurredAt EventInputParams.Platform EventInputParams.ProviderLocator EventInputParams.SourceEventID EventInputParams.Tenant EventInputParams.TraceID
		EventRef EventRef.Account EventRef.Conversation EventRef.HasLocator EventRef.Locator EventRef.Platform EventRef.SourceEvent EventRefParams EventRefParams.Account EventRefParams.Conversation EventRefParams.Locator EventRefParams.Platform EventRefParams.SourceEvent
		Extension Extension.Data Extension.LocalName Extension.MediaType Extension.NamespaceURI Extension.SchemaVersion IdempotencyKey IdempotencyKey.String Interactive Interactive.ActionID Interactive.Label Interactive.Layout InteractiveParams InteractiveParams.ActionID InteractiveParams.Label InteractiveParams.Layout Invocation Location Location.Label Location.Latitude Location.Longitude LocationParams LocationParams.Label LocationParams.Latitude LocationParams.Longitude
		Media Media.ByteSize Media.Duration Media.Extensions Media.Filename Media.Hashes Media.Height Media.MediaType Media.Role Media.Source Media.Width MediaAnimation MediaAudio MediaDocument MediaHash MediaHash.Algorithm MediaHash.Value MediaImage MediaOther MediaParams MediaParams.ByteSize MediaParams.Duration MediaParams.Extensions MediaParams.Filename MediaParams.Hashes MediaParams.Height MediaParams.MediaType MediaParams.Role MediaParams.Source MediaParams.Width MediaRole
		MediaSource MediaSource.Extensions MediaSource.ExternalURI MediaSource.Inline MediaSource.Kind MediaSource.OpaqueLocator MediaSourceExternal MediaSourceInline MediaSourceKind MediaSourceOpaque MediaSticker MediaThumbnail MediaVideo MediaVoice
		Message Message.Extensions Message.Part Message.Parts Message.Relations Message.Roots Messenger Module ModuleDescriptor ModuleDescriptor.Consumes ModuleDescriptor.ID ModuleID ModuleID.String
		NewAccountID NewActorID NewAnnotation NewCanceledResult NewCausalEventID NewContact NewContactPart NewConversationID NewEmbeddedMessage NewEmbeddedMessagePart NewEventAction NewEventID NewEventIDGenerator NewEventInput NewEventRef NewExtension NewExternalMediaSource NewFailedResult NewIdempotencyKey NewInlineMediaSource NewInteractive NewInteractivePart NewLocation NewLocationPart NewMedia NewMediaHash NewMediaPart NewMessage NewModuleDescriptor NewModuleID NewOpaqueMediaSource NewOpaquePart NewOutboundMessage NewPartID NewPlatformID NewPoll NewPollPart NewProviderLocator NewRejectedResult NewRelation NewResult NewSourceEventID NewSucceededResult NewTenantID NewTextPart NewTraceID
		OutboundMessage OutboundMessage.Account OutboundMessage.Conversation OutboundMessage.IdempotencyKey OutboundMessage.Message OutboundMessage.OriginatingEvent OutboundMessage.OriginatingModule OutboundMessage.Platform OutboundMessage.Tenant OutboundMessageParams OutboundMessageParams.Account OutboundMessageParams.Conversation OutboundMessageParams.IdempotencyKey OutboundMessageParams.Message OutboundMessageParams.OriginatingEvent OutboundMessageParams.OriginatingModule OutboundMessageParams.Platform OutboundMessageParams.Tenant
		Part Part.Children Part.Contact Part.EmbeddedMessage Part.Extensions Part.ID Part.Interactive Part.Kind Part.Location Part.Media Part.Poll Part.Text Part.Value PartContact PartEmbeddedMessage PartFile PartID PartID.String PartInteractive PartKind PartLocation PartMedia PartOpaque PartPoll PartText PlatformID PlatformID.String Poll Poll.Multiple Poll.Options Poll.Question PollParams PollParams.Multiple PollParams.Options PollParams.Question ProviderLocator ProviderLocator.Data ProviderLocator.NamespaceURI ProviderLocator.SchemaVersion
		Relation Relation.Extensions Relation.Key Relation.Kind Relation.Target RelationAnnotation RelationForward RelationKind RelationReference RelationReplacement RelationReply RelationThread Result Result.Err Result.Status ResultCanceled ResultFailed ResultRejected ResultStatus ResultSucceeded SourceEventID SourceEventID.String TenantID TenantID.String TraceID TraceID.String
	`)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exported API differs\n got: %v\nwant: %v", got, want)
	}
}

func TestPublicInterfacesHaveExactMethodShapes(t *testing.T) {
	type wantedEvent interface {
		ID() EventID
		Platform() PlatformID
		Account() AccountID
		Tenant() TenantID
		Conversation() ConversationID
		SourceEventID() (SourceEventID, bool)
		Actor() (ActorID, bool)
		Action() EventAction
		Direction() Direction
		OccurredAt() time.Time
		IngestedAt() time.Time
		EditedAt() (time.Time, bool)
		TraceID() (TraceID, bool)
		CausalEventID() (CausalEventID, bool)
		Completeness() Completeness
		ProviderLocator() (ProviderLocator, bool)
		Message() Message
		Extensions() []Extension
	}
	type wantedModule interface {
		Descriptor() ModuleDescriptor
		Handle(context.Context, Event, Invocation) Result
	}
	type wantedInvocation interface {
		Send(context.Context, string, Message) error
	}
	type wantedMessenger interface {
		Send(context.Context, OutboundMessage) error
	}

	tests := []struct {
		name string
		got  reflect.Type
		want reflect.Type
	}{
		{"Event", reflect.TypeOf((*Event)(nil)).Elem(), reflect.TypeOf((*wantedEvent)(nil)).Elem()},
		{"Module", reflect.TypeOf((*Module)(nil)).Elem(), reflect.TypeOf((*wantedModule)(nil)).Elem()},
		{"Invocation", reflect.TypeOf((*Invocation)(nil)).Elem(), reflect.TypeOf((*wantedInvocation)(nil)).Elem()},
		{"Messenger", reflect.TypeOf((*Messenger)(nil)).Elem(), reflect.TypeOf((*wantedMessenger)(nil)).Elem()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assertExactInterface(t, tt.got, tt.want) })
	}
}

func assertExactInterface(t *testing.T, got, want reflect.Type) {
	t.Helper()
	if got.NumMethod() != want.NumMethod() {
		t.Fatalf("has %d methods, want %d", got.NumMethod(), want.NumMethod())
	}
	for i := range want.NumMethod() {
		gotMethod, wantMethod := got.Method(i), want.Method(i)
		if gotMethod.Name != wantMethod.Name || gotMethod.Type != wantMethod.Type {
			t.Fatalf("method %d = %s %s, want %s %s", i, gotMethod.Name, gotMethod.Type, wantMethod.Name, wantMethod.Type)
		}
	}
}

func exportedPackageNames() ([]string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	files, err := parser.ParseDir(token.NewFileSet(), directory, func(info fs.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		return nil, err
	}
	pkg := files["otogi"]
	if pkg == nil {
		return nil, fmt.Errorf("otogi package not found in %s", directory)
	}
	var names []string
	for _, file := range pkg.Files {
		for _, declaration := range file.Decls {
			switch declaration := declaration.(type) {
			case *ast.FuncDecl:
				if !ast.IsExported(declaration.Name.Name) {
					continue
				}
				name := declaration.Name.Name
				if declaration.Recv != nil {
					name = receiverName(declaration.Recv.List[0].Type) + "." + name
				}
				names = append(names, name)
			case *ast.GenDecl:
				for _, specification := range declaration.Specs {
					switch specification := specification.(type) {
					case *ast.TypeSpec:
						if !ast.IsExported(specification.Name.Name) {
							continue
						}
						names = append(names, specification.Name.Name)
						if structure, ok := specification.Type.(*ast.StructType); ok {
							for _, field := range structure.Fields.List {
								fieldNames := field.Names
								if len(fieldNames) == 0 {
									if embedded, ok := embeddedFieldName(field.Type); ok {
										fieldNames = []*ast.Ident{ast.NewIdent(embedded)}
									}
								}
								for _, fieldName := range fieldNames {
									if ast.IsExported(fieldName.Name) {
										names = append(names, specification.Name.Name+"."+fieldName.Name)
									}
								}
							}
						}
					case *ast.ValueSpec:
						for _, name := range specification.Names {
							if ast.IsExported(name.Name) {
								names = append(names, name.Name)
							}
						}
					}
				}
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

func receiverName(expression ast.Expr) string {
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	return expression.(*ast.Ident).Name
}

func embeddedFieldName(expression ast.Expr) (string, bool) {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name, true
	case *ast.SelectorExpr:
		return expression.Sel.Name, true
	case *ast.StarExpr:
		return embeddedFieldName(expression.X)
	case *ast.IndexExpr:
		return embeddedFieldName(expression.X)
	case *ast.IndexListExpr:
		return embeddedFieldName(expression.X)
	case *ast.ParenExpr:
		return embeddedFieldName(expression.X)
	default:
		return "", false
	}
}
