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
		AccountID AccountID.String ActorID ActorID.String CausalEventID CausalEventID.String
		ConversationID ConversationID.String Event EventID EventID.String EventIDGenerator
		EventIDGenerator.Next EventInput EventInput.Account EventInput.Actor EventInput.CausalEventID
		EventInput.Conversation EventInput.IngestedAt EventInput.Kind EventInput.OccurredAt
		EventInput.Platform EventInput.SchemaVersion EventInput.SourceEventID EventInput.Tenant
		EventInput.Text EventInput.TraceID EventInputParams EventInputParams.Account
		EventInputParams.Actor EventInputParams.CausalEventID EventInputParams.Conversation
		EventInputParams.IngestedAt EventInputParams.Kind EventInputParams.OccurredAt
		EventInputParams.Platform EventInputParams.SchemaVersion EventInputParams.SourceEventID
		EventInputParams.Tenant EventInputParams.Text EventInputParams.TraceID EventKind EventKind.String
		IdempotencyKey IdempotencyKey.String Invocation Messenger Module ModuleDescriptor
		ModuleDescriptor.Consumes ModuleDescriptor.ID ModuleID ModuleID.String OutboundMessage
		OutboundMessage.Account OutboundMessage.Conversation OutboundMessage.IdempotencyKey
		OutboundMessage.OriginatingEvent OutboundMessage.OriginatingModule OutboundMessage.Platform
		OutboundMessage.Tenant OutboundMessage.Text OutboundMessageParams
		OutboundMessageParams.Account OutboundMessageParams.Conversation
		OutboundMessageParams.IdempotencyKey OutboundMessageParams.OriginatingEvent
		OutboundMessageParams.OriginatingModule OutboundMessageParams.Platform
		OutboundMessageParams.Tenant OutboundMessageParams.Text PlatformID PlatformID.String
		Result Result.Err Result.Status ResultStatus SourceEventID SourceEventID.String TenantID
		TenantID.String Text Text.String TraceID TraceID.String
		ErrCapacity ErrInvalid ErrInvalidState ErrModulePanic ErrUnavailable
		NewAccountID NewActorID NewCanceledResult NewCausalEventID NewConversationID
		NewEventID NewEventIDGenerator NewEventInput NewEventKind NewFailedResult
		NewIdempotencyKey NewModuleDescriptor NewModuleID NewOutboundMessage NewPlatformID
		NewRejectedResult NewResult NewSourceEventID NewSucceededResult NewTenantID NewText
		NewTraceID ResultCanceled ResultFailed ResultRejected ResultSucceeded
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
		Kind() EventKind
		SchemaVersion() uint64
		OccurredAt() time.Time
		IngestedAt() time.Time
		TraceID() (TraceID, bool)
		CausalEventID() (CausalEventID, bool)
		Text() Text
	}
	type wantedModule interface {
		Descriptor() ModuleDescriptor
		Handle(context.Context, Event, Invocation) Result
	}
	type wantedInvocation interface {
		SendText(context.Context, string, Text) error
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
								for _, fieldName := range field.Names {
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
