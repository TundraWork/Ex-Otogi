package kernel

import (
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

type event struct {
	id    otogi.EventID
	input otogi.EventInput
}

func (e event) ID() otogi.EventID                              { return e.id }
func (e event) Platform() otogi.PlatformID                     { return e.input.Platform() }
func (e event) Account() otogi.AccountID                       { return e.input.Account() }
func (e event) Tenant() otogi.TenantID                         { return e.input.Tenant() }
func (e event) Conversation() otogi.ConversationID             { return e.input.Conversation() }
func (e event) SourceEventID() (otogi.SourceEventID, bool)     { return e.input.SourceEventID() }
func (e event) Actor() (otogi.ActorID, bool)                   { return e.input.Actor() }
func (e event) Action() otogi.EventAction                      { return e.input.Action() }
func (e event) Direction() otogi.Direction                     { return e.input.Direction() }
func (e event) OccurredAt() time.Time                          { return e.input.OccurredAt() }
func (e event) IngestedAt() time.Time                          { return e.input.IngestedAt() }
func (e event) EditedAt() (time.Time, bool)                    { return e.input.EditedAt() }
func (e event) TraceID() (otogi.TraceID, bool)                 { return e.input.TraceID() }
func (e event) CausalEventID() (otogi.CausalEventID, bool)     { return e.input.CausalEventID() }
func (e event) Completeness() otogi.Completeness               { return e.input.Completeness() }
func (e event) ProviderLocator() (otogi.ProviderLocator, bool) { return e.input.ProviderLocator() }
func (e event) Message() otogi.Message                         { return e.input.Message() }
func (e event) Extensions() []otogi.Extension                  { return e.input.Extensions() }
