package gemini

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	ia "google.golang.org/genai/interactions/models/interactions"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/markdown"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// call is the state of one Generate: the steps of the model's turn, rebuilt
// from the stream's deltas, and what is needed to record it.
type call struct {
	started   time.Time
	partial   func(otogi.Message) error
	exchanges exchanges

	// Per attempt; see reset.
	steps       map[int]map[string]any // by step index
	order       []int
	args        map[int]string    // function call arguments streamed so far, by step
	media       map[string][]byte // decoded images, by "step/content"
	status      string
	streamErr   string
	usage       llm.Usage
	firstOutput time.Duration // until the first output of the first attempt that had any
	unhandled   []json.RawMessage

	// Across attempts: when each stream event arrived, and the interaction
	// IDs the server gave, for telling a slow server from a slow network.
	timeline     []mark
	interactions []string
}

// mark is one stream event's arrival.
type mark struct {
	At      string `json:"at"` // since the call began
	Attempt int    `json:"attempt"`
	Event   string `json:"event"` // the event type, and a delta's type
	Index   *int   `json:"index,omitempty"`
	Bytes   int    `json:"bytes"`

	after time.Duration
}

// silence is the longest stretch without a stream event.
type silence struct {
	For   string `json:"for"`
	After string `json:"after"` // the event before it, or "request" from the start
	At    string `json:"at"`    // when it began, since the call began
}

// longestSilence finds the longest gap between the call's start, its
// events, and end.
func (c *call) longestSilence(end time.Duration) silence {
	var best silence
	var bestFor time.Duration
	prev, prevName := time.Duration(0), "request"
	for _, m := range append(c.timeline, mark{after: end, Event: "end"}) {
		if gap := m.after - prev; gap > bestFor {
			bestFor = gap
			best = silence{For: gap.Round(time.Millisecond).String(), After: prevName, At: prev.Round(time.Millisecond).String()}
		}
		prev, prevName = m.after, m.Event
	}
	return best
}

// reset starts an attempt afresh.
func (c *call) reset() {
	c.steps, c.order, c.media, c.args = map[int]map[string]any{}, nil, map[string][]byte{}, map[int]string{}
	c.status, c.streamErr, c.usage, c.unhandled = "", "", llm.Usage{}, nil
}

// note puts a stream event on the timeline.
func (c *call) note(e *ia.InteractionSSEStreamEvent) {
	d := e.Data
	m := mark{Attempt: c.exchanges.count(), Event: string(d.Type), after: time.Since(c.started)}
	m.At = m.after.Round(time.Millisecond).String()
	switch {
	case d.StepStart != nil:
		m.Index = &d.StepStart.Index
	case d.StepDelta != nil:
		m.Index, m.Event = &d.StepDelta.Index, m.Event+":"+string(d.StepDelta.Delta.Type)
	case d.StepStop != nil:
		m.Index = &d.StepStop.Index
	case d.InteractionStatusUpdate != nil:
		m.Event += ":" + string(d.InteractionStatusUpdate.Status)
	}
	if raw, err := json.Marshal(d); err == nil {
		m.Bytes = len(raw)
	}
	c.timeline = append(c.timeline, m)
	if ev := d.InteractionCreatedEvent; ev != nil {
		c.addInteraction(ev.Interaction)
	}
	if ev := d.InteractionCompletedEvent; ev != nil {
		c.addInteraction(ev.Interaction)
	}
}

// addInteraction keeps the ID of an interaction the server reported.
func (c *call) addInteraction(interaction any) {
	fields, _ := object(interaction)
	if id, _ := fields["id"].(string); id != "" && !slices.Contains(c.interactions, id) {
		c.interactions = append(c.interactions, id)
	}
}

// event applies one stream event.
func (c *call) event(e *ia.InteractionSSEStreamEvent) error {
	if e == nil {
		return nil
	}
	c.note(e)
	d := e.Data
	switch {
	case d.StepStart != nil:
		step, err := object(d.StepStart.Step)
		if err != nil {
			return err
		}
		if _, ok := c.steps[d.StepStart.Index]; !ok {
			c.order = append(c.order, d.StepStart.Index)
		}
		c.steps[d.StepStart.Index] = step
	case d.StepDelta != nil:
		return c.delta(d.StepDelta)
	case d.InteractionCompletedEvent != nil:
		i := d.InteractionCompletedEvent.Interaction
		c.status = string(i.Status)
		if i.Usage != nil {
			c.usage = usageOf(*i.Usage)
		}
	case d.InteractionStatusUpdate != nil:
		c.status = string(d.InteractionStatusUpdate.Status)
	case d.ErrorEvent != nil:
		raw, _ := json.Marshal(d.ErrorEvent.Error)
		c.streamErr = "stream error " + string(raw)
	case d.StepStop != nil:
		c.finishArguments(d.StepStop.Index)
	case d.InteractionCreatedEvent != nil:
	default:
		c.unhandled = append(c.unhandled, d.UnknownRaw)
	}
	return nil
}

// delta applies one step delta. Text and media extend the step's content
// (text joins the text before it); a thought's signature and summary fill
// in the thought.
func (c *call) delta(d *ia.StepDelta) error {
	step, ok := c.steps[d.Index]
	if !ok { // a delta before its step.start: keep it as a step of its own
		step = map[string]any{}
		c.steps[d.Index] = step
		c.order = append(c.order, d.Index)
	}
	delta, err := object(d.Delta)
	if err != nil {
		return err
	}
	kind, _ := delta["type"].(string)
	switch kind {
	case "thought_signature":
		step["signature"] = delta["signature"]
		return nil
	case "thought_summary":
		if content, ok := delta["content"].(map[string]any); ok {
			step["summary"] = appendContent(step["summary"], content)
		}
		return nil
	case "text", "image", "audio", "video", "document":
		step["content"] = appendContent(step["content"], delta)
	case "arguments_delta":
		fragment, _ := delta["arguments"].(string)
		c.args[d.Index] += fragment
		return nil
	case "text_annotation_delta":
		blocks, _ := step["content"].([]any)
		if len(blocks) > 0 {
			if last, ok := blocks[len(blocks)-1].(map[string]any); ok {
				notes, _ := delta["annotations"].([]any)
				existing, _ := last["annotations"].([]any)
				last["annotations"] = append(existing, notes...)
			}
		}
		return nil
	default:
		if kind != "" && kind == step["type"] { // a built-in tool's call or result
			for k, v := range delta {
				if k != "type" {
					step[k] = v
				}
			}
			return nil
		}
		raw, _ := json.Marshal(d)
		c.unhandled = append(c.unhandled, raw)
		return nil
	}
	if step["type"] != "model_output" {
		return nil
	}
	if c.firstOutput == 0 {
		c.firstOutput = time.Since(c.started)
	}
	if c.partial == nil {
		return nil
	}
	reply, _ := c.reply()
	if err := c.partial(reply); err != nil {
		return fmt.Errorf("gemini: %w: %w", errStopped, err)
	}
	return nil
}

// finishArguments sets a function call's arguments from its streamed
// fragments. Fragments that do not parse are kept as a string, so the model
// sees its own malformed call.
func (c *call) finishArguments(index int) {
	raw, ok := c.args[index]
	if !ok {
		return
	}
	delete(c.args, index)
	var args any
	if json.Unmarshal([]byte(raw), &args) != nil {
		args = raw
	}
	if step, ok := c.steps[index]; ok {
		step["arguments"] = args
	}
}

// appendContent appends block to a content list, joining consecutive text.
func appendContent(list any, block map[string]any) []any {
	blocks, _ := list.([]any)
	if block["type"] == "text" && len(blocks) > 0 {
		if last, ok := blocks[len(blocks)-1].(map[string]any); ok && last["type"] == "text" {
			text, _ := block["text"].(string)
			prev, _ := last["text"].(string)
			last["text"] = prev + text
			return blocks
		}
	}
	return append(blocks, block)
}

// stepList is the turn's steps in stream order.
func (c *call) stepList() []any {
	out := make([]any, 0, len(c.order))
	for _, i := range c.order {
		out = append(out, c.steps[i])
	}
	return out
}

// reply is the model's output so far as a message, and its Markdown: text
// runs rendered to rich text, images as media parts, in order.
func (c *call) reply() (otogi.Message, string) {
	var msg otogi.Message
	var all []string
	var run strings.Builder
	add := func(p otogi.Part) {
		p.ID = otogi.PartID("p" + strconv.Itoa(len(msg.Parts)))
		msg.Parts = append(msg.Parts, p)
		msg.Roots = append(msg.Roots, p.ID)
	}
	flush := func() {
		if strings.TrimSpace(run.String()) == "" {
			run.Reset()
			return
		}
		all = append(all, run.String())
		rich := markdown.Render(run.String())
		add(otogi.Part{Kind: otogi.PartText, Text: rich.String(), Annotations: rich.Annotations()})
		run.Reset()
	}
	for _, i := range c.order {
		step := c.steps[i]
		if step["type"] != "model_output" {
			continue
		}
		blocks, _ := step["content"].([]any)
		for j, b := range blocks {
			block, _ := b.(map[string]any)
			switch block["type"] {
			case "text":
				text, _ := block["text"].(string)
				run.WriteString(text)
			case "image":
				flush()
				if media := c.image(fmt.Sprintf("%d/%d", i, j), block); media != nil {
					add(otogi.Part{Kind: otogi.PartMedia, Media: media})
				}
			}
		}
	}
	flush()
	return msg, strings.Join(all, "\n\n")
}

// image decodes an image block once.
func (c *call) image(key string, block map[string]any) *otogi.Media {
	data, ok := c.media[key]
	if !ok {
		encoded, _ := block["data"].(string)
		var err error
		if data, err = base64.StdEncoding.DecodeString(encoded); err != nil || len(data) == 0 {
			return nil
		}
		c.media[key] = data
	}
	mime, _ := block["mime_type"].(string)
	if mime == "" { // image deltas may leave it out
		mime, _, _ = strings.Cut(http.DetectContentType(data), ";")
	}
	return &otogi.Media{Role: otogi.MediaImage, MediaType: mime, ByteSize: int64(len(data)), Hashes: []otogi.MediaHash{sha256Hash(data)}, Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: data}}
}

// response is the finished turn.
func (c *call) response() llm.Response {
	for index := range c.args {
		c.finishArguments(index)
	}
	reply, md := c.reply()
	steps, _ := json.Marshal(c.stepList())
	finish := finishOf(c.status)
	resp := llm.Response{
		Turn:     llm.Turn{Role: llm.RoleModel, Content: reply, Native: &llm.Native{Provider: Name, Data: steps}},
		Markdown: md, Finish: finish, Usage: c.usage,
	}
	seen := map[string]bool{}
	for _, i := range c.order {
		step := c.steps[i]
		switch step["type"] {
		case "function_call":
			id, _ := step["id"].(string)
			name, _ := step["name"].(string)
			args, _ := json.Marshal(step["arguments"])
			resp.Turn.Calls = append(resp.Turn.Calls, llm.Call{ID: id, Name: name, Args: args})
		case "model_output":
			blocks, _ := step["content"].([]any)
			for _, b := range blocks {
				block, _ := b.(map[string]any)
				notes, _ := block["annotations"].([]any)
				for _, n := range notes {
					note, _ := n.(map[string]any)
					url, _ := note["url"].(string)
					if note["type"] != "url_citation" || url == "" || seen[url] {
						continue
					}
					seen[url] = true
					title, _ := note["title"].(string)
					resp.Sources = append(resp.Sources, llm.Source{Title: title, URL: url})
				}
			}
		}
	}
	if len(resp.Turn.Calls) > 0 {
		resp.Finish = llm.FinishToolCalls
	}
	if resp.Finish != llm.FinishStop && resp.Finish != llm.FinishToolCalls {
		resp.Reason = c.status
	}
	return resp
}

func usageOf(u ia.Usage) llm.Usage {
	n := func(p *int) int {
		if p == nil {
			return 0
		}
		return *p
	}
	return llm.Usage{Input: n(u.TotalInputTokens), Cached: n(u.TotalCachedTokens), Output: n(u.TotalOutputTokens), Thoughts: n(u.TotalThoughtTokens)}
}

// object is v's JSON form as a map.
func object(v any) (map[string]any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("gemini: encode %T: %w", v, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("gemini: decode %T: %w", v, err)
	}
	return m, nil
}
