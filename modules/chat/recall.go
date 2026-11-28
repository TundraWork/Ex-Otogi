package chat

// An agent with a memory block reads its memory of a chat only through
// recall_memory: it says what it wants to recall, and the memory model,
// reading the whole book with the recent chat, picks the entries that help,
// or none. Of the memory, a reply's prompt has only a note ending the turn
// that names whom it has notes about, so what has nothing to do with the
// chat does not come up.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// recallTool is the function an agent with a memory block recalls with. It
// does not end the reply: what it recalls comes back for the agent to write
// its reply with.
const recallTool = "recall_memory"

const (
	// recallMost is the most entries one recall returns.
	recallMost = 5
	// recallRecent is how many of the chat's last messages the memory model
	// reads with what the agent wants to recall.
	recallRecent = 8
	// recallTimeout bounds one recall: a request to the memory model, about 5
	// seconds with Eku's book of 150 entries.
	recallTimeout = 30 * time.Second
)

// What a recall request says, under these headers.
const (
	recallChatHeader = "The recent chat:"
	recallBookHeader = "Its memory of this chat (number, date noted):"
)

// What the agent is told of a recall.
const (
	rememberedHeader = "What you remember, each with the date it was noted. It may be out of date: where someone now says otherwise, go by what they say."
	recallNothing    = "Nothing you remember fits."
)

// errNotRecalled is what the agent is told of a recall that failed.
var errNotRecalled = errors.New("you could not recall it this time; reply without it")

// recaller is recall_memory in one reply.
type recaller struct {
	m     *Module
	p     *profile
	to    otogi.Destination
	names []string // whom the memory has entries about, as they are called now
}

// recallerFor is p's recall_memory in the chat to; nil when it remembers
// nothing there.
func (m *Module) recallerFor(ctx context.Context, p *profile, to otogi.Destination) (*recaller, error) {
	book, err := m.store.book(ctx, p.Name, to)
	if err != nil || len(book) == 0 {
		return nil, err
	}
	names, _, err := m.namesNow(ctx, to, book)
	if err != nil {
		return nil, err
	}
	return &recaller{m: m, p: p, to: to, names: names}, nil
}

// note ends the agent's turn, saying whom its memory has notes about. In
// the turn, like lookupNote, it gets the memory looked up when a line only
// hints at something remembered. With the names in the tool's description
// instead, DeepSeek (low) recalled on 2 of 45 such lines and on 1 of 18 real
// ones that needed its memory; with this note, on 33 of 45 and 9 of 18, and
// on 32 of 639 real lines that needed nothing (Eku's chats on dsh). Asked
// outright ("do you remember why I don't drink coffee?"), it recalled 24 of
// 27 times either way.
func (r *recaller) note() string {
	about := "the chat's running jokes and agreements"
	if len(r.names) > 0 {
		about = strings.Join(r.names, ", ") + ", and " + about
	}
	return "(Your memory of this chat is not in front of you: you have notes about " + about + ". Look up what you need with " + recallTool + ".)"
}

func (r *recaller) tool() llm.Tool {
	return llm.NewTool(llm.Function{
		Name: recallTool,
		Description: "Recall what you remember of this chat from before the recent messages: something about a person, the chat's running jokes and agreements, or what someone asked of you. " +
			"Say in a sentence what you want to recall. For general knowledge and news, search instead.",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"what":{"type":"string","description":"what you want to recall, in a sentence"}},"required":["what"]}`),
	}, func(ctx context.Context, _ otogi.Event, args json.RawMessage) (any, error) {
		var in struct {
			What string `json:"what"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf(`want {"what": ...}: %w`, err)
		}
		if strings.TrimSpace(in.What) == "" {
			return nil, errors.New("say what you want to recall")
		}
		ctx, cancel := context.WithTimeout(ctx, recallTimeout)
		defer cancel()
		text, err := r.recall(ctx, in.What)
		if err != nil {
			return nil, err
		}
		return map[string]string{"remembered": text}, nil
	})
}

// recall asks the memory model which entries help with what, and reviews
// those it picks: recalled, they are kept longer.
func (r *recaller) recall(ctx context.Context, what string) (string, error) {
	m, p, to := r.m, r.p, r.to
	started := m.now()
	book, err := m.store.book(ctx, p.Name, to)
	if err != nil {
		return "", err
	}
	recent, err := m.store.newest(ctx, to, recallRecent)
	if err != nil {
		return "", err
	}
	chat, err := m.memoryLines(ctx, p, to, recent, nil)
	if err != nil {
		return "", err
	}
	entries := make([]string, 0, len(book))
	for _, e := range book {
		entries = append(entries, fmt.Sprintf("%d. [%s] %s", e.ID, e.Created.In(time.Local).Format("2006-01-02"), e.Text))
	}
	content := fmt.Sprintf("What %s wants to recall: %s\n\n%s\n%s\n\n%s\n%s", p.title(), what, recallChatHeader, chat, recallBookHeader, strings.Join(entries, "\n"))
	payload := map[string]any{"profile": p.Name, "action": "recall", "what": what, "model": p.memory.model, "entries": len(book)}
	resp, err := p.memory.provider.Generate(ctx, m.memoryRequest(p, recallRules, content), nil)
	if err != nil {
		payload["failure"], payload["error"] = "request", err.Error()
		m.recordMemory(ctx, to, "error", started, payload)
		return "", errNotRecalled
	}
	payload["usage"] = resp.Usage
	ids, none := parseRecall(resp.Markdown)
	var picked []memoryEntry
	for _, id := range ids {
		i := slices.IndexFunc(book, func(e memoryEntry) bool { return e.ID == id })
		if i >= 0 && !slices.ContainsFunc(picked, func(e memoryEntry) bool { return e.ID == id }) && len(picked) < recallMost {
			picked = append(picked, book[i])
		}
	}
	if resp.Finish == llm.FinishBlocked || len(picked) == 0 && !none {
		payload["failure"], payload["answer"] = "unusable", resp.Markdown
		if resp.Finish == llm.FinishBlocked {
			payload["blocked"] = resp.Reason
		}
		m.recordMemory(ctx, to, "error", started, payload)
		return "", errNotRecalled
	}
	pickedIDs := make([]int64, 0, len(picked))
	gave := make([]map[string]any, 0, len(picked))
	for _, e := range picked {
		pickedIDs = append(pickedIDs, e.ID)
		gave = append(gave, map[string]any{"id": e.ID, "text": e.Text})
	}
	// Only the review is written: a batch running meanwhile may have
	// rewritten or merged what was read before the request.
	if err := m.store.reviewMemory(ctx, pickedIDs, started, memoryRecallBoost); err != nil {
		return "", err
	}
	payload["picked"] = gave
	m.recordMemory(ctx, to, "ok", started, payload)
	if len(picked) == 0 {
		return recallNothing, nil
	}
	return m.remembered(ctx, to, picked)
}

var recallID = regexp.MustCompile(`^#?(\d+)\.?$`)

// parseRecall reads a recall answer: the entries it names, in order, and
// whether it says none.
func parseRecall(answer string) (ids []int64, none bool) {
	for _, raw := range strings.Split(answer, "\n") {
		l := strings.TrimSpace(raw)
		if strings.EqualFold(strings.TrimRight(l, "."), "none") {
			none = true
			continue
		}
		if n := recallID.FindStringSubmatch(l); n != nil {
			if id, err := strconv.ParseInt(n[1], 10, 64); err == nil {
				ids = append(ids, id)
			}
		}
	}
	return ids, none
}

// remembered is entries as the agent is told them, each with the date it
// was noted, ending with the people whose name now differs from the one
// noted with an entry about them.
func (m *Module) remembered(ctx context.Context, to otogi.Destination, entries []memoryEntry) (string, error) {
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		// The memory is the model's words on what people wrote, which may
		// carry the operator mark: only names the bot gives may show it.
		lines = append(lines, fmt.Sprintf("- [%s] %s", e.Created.In(time.Local).Format("2006-01-02"), withoutOperatorMark(e.Text)))
	}
	_, renamed, err := m.namesNow(ctx, to, entries)
	if err != nil {
		return "", err
	}
	text := rememberedHeader + "\n" + strings.Join(lines, "\n")
	if len(renamed) > 0 {
		text += "\nNames noted then that differ now: " + strings.Join(renamed, "; ") + "."
	}
	return text, nil
}

// namesNow is whom entries are about, as they are called now, and, for
// those whose name now differs from the one noted, "noted is now name":
// their name in the people list, or the one they last sent a message there
// under. Names are without the group's marks, which the prompt explains
// only for turns that show them.
func (m *Module) namesNow(ctx context.Context, to otogi.Destination, entries []memoryEntry) (names, renamed []string, err error) {
	noted := map[[2]string]bool{} // actor and a name noted for them
	var actors []string
	for _, e := range entries {
		for id, name := range e.People {
			if name != "" && !noted[[2]string{id, name}] {
				noted[[2]string{id, name}] = true
				actors = append(actors, id)
			}
		}
	}
	slices.Sort(actors)
	actors = slices.Compact(actors)
	shown, err := m.store.shownNames(ctx, to, actors)
	if err != nil {
		return nil, nil, err
	}
	// A standing is no new name: an entry noted before someone became an
	// operator, the group's owner or an admin, or stopped being one, names
	// them still.
	noRole := strings.NewReplacer(" "+ownerMark, "", " "+adminMark, "").Replace
	bare := func(name string) string { return noRole(strings.TrimSuffix(name, " "+operatorMark)) }
	current := map[string]string{} // actor → name now
	for k := range noted {
		s, seen := shown[k[0]]
		if who, ok := m.known(to.Platform, k[0]); !seen && (!ok || who.name == "") {
			if current[k[0]] == "" {
				current[k[0]] = noRole(k[1]) // nothing says what they are called now
			}
			continue
		}
		now := m.nameOf(to.Platform, k[0], s, "")
		current[k[0]] = now
		if bare(now) == bare(k[1]) {
			continue
		}
		// With no message left in the chat, only the list's half of the name
		// is known now: "Captain (Bob)" noted is no other name than "Captain".
		if old := bare(k[1]); !seen && strings.HasPrefix(old, bare(now)+" (") && strings.HasSuffix(old, ")") {
			continue
		}
		renamed = append(renamed, noRole(k[1])+" is now "+now)
	}
	for _, a := range actors {
		names = append(names, current[a])
	}
	slices.Sort(names)
	slices.Sort(renamed)
	return slices.Compact(names), renamed, nil
}

// recallRules is the system prompt of a recall request; {agent} is the
// agent.
const recallRules = `You are the memory {agent}, a chatbot, keeps of one chat. {agent} is replying there and wants to recall something. Pick the entries of its memory that help with what it wants to recall, given the recent chat: at most 5, each as its number alone on a line. An entry only about the same person, and not about what it wants to recall, does not help.
With none that helps, answer only: none`
