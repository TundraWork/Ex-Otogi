package management

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/management/queries"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Chat is a chat the bot has seen.
type Chat struct {
	Platform     string    `json:"platform"`
	Conversation string    `json:"conversation"`
	Account      string    `json:"account"` // the account that saw it last
	Kind         string    `json:"kind,omitempty"`
	Title        string    `json:"title,omitempty"` // as the platform names it
	Name         string    `json:"name,omitempty"`  // as the operator names it
	Description  string    `json:"description,omitempty"`
	Members      int       `json:"members,omitempty" doc:"How many members it has, as the platform last said."`
	People       int       `json:"people" doc:"How many people the bot has seen sending messages in it."`
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
	// LastMessage is the event.received record of its latest new message,
	// unless that is older than the retention.
	LastMessage *Entry `json:"last_message,omitempty"`
}

// Key is the chat as /whoami prints it and allowlists name it.
func (c Chat) Key() string { return c.Platform + ":" + c.Conversation }

// ChatDay is how many new messages a chat had on one day (UTC).
type ChatDay struct {
	Day      string `json:"day"` // YYYY-MM-DD
	Messages int64  `json:"messages"`
}

func day(t time.Time) string { return t.UTC().Format(time.DateOnly) }

// seeChat updates the directory with a received event: the chat's facts,
// and for a new message, its latest message and the day's count.
func seeChat(ctx context.Context, q *queries.Queries, e otogi.Event, at time.Time, record int64) error {
	var last int64
	if e.Action == otogi.ActionMessageCreated {
		last = record
	}
	err := q.SeeChat(ctx, queries.SeeChatParams{
		Platform: string(e.Platform), Conversation: string(e.Conversation.ID), Account: string(e.Account),
		Kind: string(e.Conversation.Kind), Title: e.Conversation.Title, Description: e.Conversation.Description,
		Members: int64(e.Conversation.Members), At: at.UnixMilli(), LastMessage: last,
	})
	if err == nil && last != 0 {
		err = q.CountMessage(ctx, queries.CountMessageParams{Platform: string(e.Platform), Conversation: string(e.Conversation.ID), Day: day(at)})
	}
	if err == nil && last != 0 && e.Direction == otogi.DirectionIncoming && e.Actor.ID != "" {
		err = q.SeePerson(ctx, queries.SeePersonParams{
			Platform: string(e.Platform), Conversation: string(e.Conversation.ID), Actor: string(e.Actor.ID), Name: e.Actor.Name, At: at.UnixMilli(),
		})
	}
	if err != nil {
		return fmt.Errorf("chat directory: %w", err)
	}
	return nil
}

// Chats returns every chat the bot has seen, most recently seen first.
func (l *EventLog) Chats(ctx context.Context) ([]Chat, error) {
	rows, err := queries.New(l.db).Chats(ctx)
	if err != nil {
		return nil, fmt.Errorf("event log: chats: %w", err)
	}
	out := make([]Chat, len(rows))
	for i, r := range rows {
		out[i] = Chat{
			Platform: r.Platform, Conversation: r.Conversation, Account: r.Account, Kind: r.Kind, Title: r.Title, Name: r.Name,
			Description: r.Description, Members: int(r.Members), People: int(r.People),
			FirstSeen: time.UnixMilli(r.FirstSeen).UTC(), LastSeen: time.UnixMilli(r.LastSeen).UTC(),
		}
		if r.MessageID.Valid {
			out[i].LastMessage = &Entry{
				ID: r.MessageID.Int64, At: time.UnixMilli(r.MessageAt.Int64).UTC(), Kind: "event.received", Component: "kernel",
				Platform: r.Platform, Account: r.Account, Conversation: r.Conversation, Payload: json.RawMessage(r.Message.String),
			}
		}
	}
	return out, nil
}

// NameChat sets the name the operator gives a chat; "" clears it. It
// reports whether the chat is known.
func (l *EventLog) NameChat(ctx context.Context, platform, conversation, name string) (bool, error) {
	n, err := queries.New(l.db).NameChat(ctx, queries.NameChatParams{Platform: platform, Conversation: conversation, Name: name})
	if err != nil {
		return false, fmt.Errorf("event log: name chat: %w", err)
	}
	return n > 0, nil
}

// Person is someone seen sending messages in a chat.
type Person struct {
	Actor     string    `json:"actor"`
	Name      string    `json:"name,omitempty" doc:"The latest name they were seen with."`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Messages  int64     `json:"messages"`
}

// ChatPeople returns the people seen in a chat, most recently seen first.
func (l *EventLog) ChatPeople(ctx context.Context, platform, conversation string, limit int) ([]Person, error) {
	rows, err := queries.New(l.db).ChatPeople(ctx, queries.ChatPeopleParams{Platform: platform, Conversation: conversation, Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("event log: chat people: %w", err)
	}
	out := make([]Person, len(rows))
	for i, r := range rows {
		out[i] = Person{Actor: r.Actor, Name: r.Name, FirstSeen: time.UnixMilli(r.FirstSeen).UTC(), LastSeen: time.UnixMilli(r.LastSeen).UTC(), Messages: r.Messages}
	}
	return out, nil
}

// SeenPerson is someone the bot has seen sending messages, in any chat.
type SeenPerson struct {
	ID       string       `json:"id" doc:"The platform-qualified actor ID, as operators and the chat module's people name them, such as telegram:user:123."`
	Name     string       `json:"name,omitempty" doc:"The latest name they were seen with, in any chat."`
	LastSeen time.Time    `json:"last_seen"`
	Chats    []PersonChat `json:"chats" nullable:"false" doc:"The chats they were last seen in, most recent first, up to 3."`
}

// PersonChat is a chat someone was seen in.
type PersonChat struct {
	ID       string    `json:"id" doc:"The chat as /whoami prints it."`
	Name     string    `json:"name,omitempty" doc:"The name the operator gave it, else the platform's latest title; absent when it has neither."`
	LastSeen time.Time `json:"last_seen" doc:"When they were last seen in it."`
}

// People finds the people seen in any chat by their platform-qualified ID
// or any name they were seen with, ignoring ASCII case, each once: the one
// whose ID is q first, then the most recently seen, up to limit. An empty q
// finds everyone.
func (l *EventLog) People(ctx context.Context, q string, limit int) ([]SeenPerson, error) {
	db := queries.New(l.db)
	rows, err := db.People(ctx, queries.PeopleParams{Q: strings.TrimSpace(q), Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("event log: people: %w", err)
	}
	out := make([]SeenPerson, len(rows))
	for i, r := range rows {
		chats, err := db.PersonChats(ctx, queries.PersonChatsParams{Platform: r.Platform, Actor: r.Actor})
		if err != nil {
			return nil, fmt.Errorf("event log: person's chats: %w", err)
		}
		p := SeenPerson{ID: r.Platform + ":" + r.Actor, LastSeen: time.UnixMilli(r.LastSeen).UTC(), Chats: []PersonChat{}}
		for _, c := range chats {
			if p.Name == "" {
				p.Name = c.Name
			}
			if len(p.Chats) < 3 {
				p.Chats = append(p.Chats, PersonChat{ID: r.Platform + ":" + c.Conversation, Name: cmp.Or(c.ChatName, c.ChatTitle), LastSeen: time.UnixMilli(c.LastSeen).UTC()})
			}
		}
		out[i] = p
	}
	return out, nil
}

// Names returns what a chat and a person are called: the chat by the name
// the operator gave it, else the platform's latest title, and the person
// by the latest name they were seen with anywhere. Unknown ones are "".
func (l *EventLog) Names(ctx context.Context, platform, conversation, actor string) (chat, person string, err error) {
	q := queries.New(l.db)
	c, err := q.ChatName(ctx, queries.ChatNameParams{Platform: platform, Conversation: conversation})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("event log: chat name: %w", err)
	}
	chat = cmp.Or(c.Name, c.Title)
	if actor != "" {
		person, err = q.PersonName(ctx, queries.PersonNameParams{Platform: platform, Actor: actor})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", "", fmt.Errorf("event log: person name: %w", err)
		}
	}
	return chat, person, nil
}

// ChatDays returns a chat's daily new messages since a day, oldest first;
// days without messages are left out.
func (l *EventLog) ChatDays(ctx context.Context, platform, conversation string, since time.Time) ([]ChatDay, error) {
	rows, err := queries.New(l.db).ChatDays(ctx, queries.ChatDaysParams{Platform: platform, Conversation: conversation, Since: day(since)})
	if err != nil {
		return nil, fmt.Errorf("event log: chat days: %w", err)
	}
	out := make([]ChatDay, len(rows))
	for i, r := range rows {
		out[i] = ChatDay{Day: r.Day, Messages: r.Messages}
	}
	return out, nil
}

// ChatMessage is one message in a chat's conversation: a person's, from its
// event.received record, or the bot's, from the effect.applied record of a
// send or a stream. Media parts come without their bytes; ChatMedia serves
// them.
type ChatMessage struct {
	ID      int64         `json:"id"` // the record's; the cursor
	At      time.Time     `json:"at"`
	Event   string        `json:"event,omitempty"` // the received event, or the event the bot answered
	Bot     bool          `json:"bot"`
	Actor   otogi.Actor   `json:"actor,omitzero"`
	Source  string        `json:"source,omitempty"` // the platform's message ID
	Message otogi.Message `json:"message"`
	// Module sent the bot's message; Records are the module's records for
	// the event it answered, such as chat.reply (with the model) and
	// module.completed (with the duration).
	Module  string  `json:"module,omitempty"`
	Records []Entry `json:"records,omitempty"`
}

// sent is the payload of an effect.applied record.
type sent struct {
	Effect otogi.Effect     `json:"effect"`
	Ref    otogi.EventRef   `json:"ref"`
	Refs   []otogi.EventRef `json:"refs"`
}

const chatMessages = `SELECT id, at, kind, component, event, platform, account, conversation, outcome, duration_ms, payload FROM records
	WHERE platform = ? AND conversation = ? AND id < ? AND (
		(kind = 'event.received' AND json_extract(payload, '$.action') = 'message.created' AND IFNULL(json_extract(payload, '$.direction'), '') <> 'outgoing')
		OR (kind = 'effect.applied' AND json_extract(payload, '$.effect.kind') IN ('send', 'stream')))
	ORDER BY id DESC LIMIT ?`

// ChatMessages returns a chat's messages older than the record before (0:
// the newest), newest first, and whether older ones remain. The bot's own
// messages come from what it sent, so a platform's echo of them is left out.
func (l *EventLog) ChatMessages(ctx context.Context, platform, conversation string, before int64, limit int) ([]ChatMessage, bool, error) {
	if before <= 0 {
		before = 1<<63 - 1
	}
	limit = min(max(limit, 1), MaxPageSize)
	entries, err := l.scan(ctx, chatMessages, platform, conversation, before, limit+1)
	if err != nil {
		return nil, false, err
	}
	more := len(entries) > limit
	entries = entries[:min(len(entries), limit)]
	out := make([]ChatMessage, 0, len(entries))
	answered := map[string]bool{}
	for _, e := range entries {
		m, err := chatMessage(e)
		if err != nil {
			return nil, false, err
		}
		if m.Bot && m.Event != "" {
			answered[m.Event] = true
		}
		out = append(out, m)
	}
	records, err := l.moduleRecords(ctx, answered)
	if err != nil {
		return nil, false, err
	}
	for i, m := range out {
		if m.Bot {
			out[i].Records = records[m.Event+"\x00module:"+m.Module]
		}
	}
	return out, more, nil
}

func chatMessage(e Entry) (ChatMessage, error) {
	m := ChatMessage{ID: e.ID, At: e.At, Event: e.Event}
	if e.Kind == "event.received" {
		var ev otogi.Event
		if err := json.Unmarshal(e.Payload, &ev); err != nil {
			return ChatMessage{}, fmt.Errorf("event log: record %d: %w", e.ID, err)
		}
		m.Actor, m.Source, m.Message = ev.Actor, string(ev.SourceID), ev.Message
	} else {
		var s sent
		if err := json.Unmarshal(e.Payload, &s); err != nil {
			return ChatMessage{}, fmt.Errorf("event log: record %d: %w", e.ID, err)
		}
		m.Bot, m.Module, m.Message, m.Source = true, string(s.Effect.Module), s.Effect.Message, string(s.Ref.SourceEvent)
		if len(s.Refs) > 0 {
			m.Source = string(s.Refs[0].SourceEvent)
		}
	}
	for i := range m.Message.Parts {
		if md := m.Message.Parts[i].Media; md != nil {
			media := *md
			media.Source.Inline = nil
			m.Message.Parts[i].Media = &media
		}
	}
	return m, nil
}

// moduleRecords returns the records modules wrote for the given events,
// other than their effects, by event and component.
func (l *EventLog) moduleRecords(ctx context.Context, events map[string]bool) (map[string][]Entry, error) {
	if len(events) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(events))
	for e := range events {
		args = append(args, e)
	}
	entries, err := l.scan(ctx, `SELECT id, at, kind, component, event, platform, account, conversation, outcome, duration_ms, payload FROM records
		WHERE event IN (?`+strings.Repeat(", ?", len(args)-1)+`) AND component LIKE 'module:%' AND kind NOT LIKE 'effect.%' ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	out := map[string][]Entry{}
	for _, e := range entries {
		key := e.Event + "\x00" + e.Component
		out[key] = append(out[key], e)
	}
	return out, nil
}

// ChatMedia returns a media part of a message in a chat, by its record and
// part ID, and the conversation it is in, for fetching it through the
// driver when its bytes are not inline.
func (l *EventLog) ChatMedia(ctx context.Context, platform, conversation string, record int64, part otogi.PartID) (otogi.Destination, otogi.Media, bool, error) {
	entries, err := l.scan(ctx, `SELECT id, at, kind, component, event, platform, account, conversation, outcome, duration_ms, payload FROM records
		WHERE id = ? AND platform = ? AND conversation = ? AND kind IN ('event.received', 'effect.applied')`, record, platform, conversation)
	if err != nil || len(entries) == 0 {
		return otogi.Destination{}, otogi.Media{}, false, err
	}
	e := entries[0]
	var msg otogi.Message
	if e.Kind == "event.received" {
		var ev otogi.Event
		err = json.Unmarshal(e.Payload, &ev)
		msg = ev.Message
	} else {
		var s sent
		err = json.Unmarshal(e.Payload, &s)
		msg = s.Effect.Message
	}
	if err != nil {
		return otogi.Destination{}, otogi.Media{}, false, fmt.Errorf("event log: record %d: %w", e.ID, err)
	}
	for _, p := range msg.Parts {
		if p.ID == part && p.Media != nil {
			to := otogi.Destination{Platform: otogi.PlatformID(e.Platform), Account: otogi.AccountID(e.Account), Conversation: otogi.ConversationID(e.Conversation)}
			return to, *p.Media, true, nil
		}
	}
	return otogi.Destination{}, otogi.Media{}, false, nil
}
