package gemini

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	media       map[string][]byte // decoded images, by "step/content"
	status      string
	streamErr   string
	usage       llm.Usage
	firstOutput time.Duration // until the first output of the first attempt that had any
	unhandled   []json.RawMessage
}

// reset starts an attempt afresh.
func (c *call) reset() {
	c.steps, c.order, c.media = map[int]map[string]any{}, nil, map[string][]byte{}
	c.status, c.streamErr, c.usage, c.unhandled = "", "", llm.Usage{}, nil
}

// event applies one stream event.
func (c *call) event(e *ia.InteractionSSEStreamEvent) error {
	if e == nil {
		return nil
	}
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
	case d.StepStop != nil, d.InteractionCreatedEvent != nil:
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
	default:
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
	return &otogi.Media{Role: otogi.MediaImage, MediaType: mime, ByteSize: int64(len(data)), Hashes: []otogi.MediaHash{sha256Hash(data)}, Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: data}}
}

// response is the finished turn.
func (c *call) response() llm.Response {
	reply, md := c.reply()
	steps, _ := json.Marshal(c.stepList())
	finish := finishOf(c.status)
	resp := llm.Response{
		Turn:     llm.Turn{Role: llm.RoleModel, Content: reply, Native: &llm.Native{Provider: Name, Data: steps}},
		Markdown: md, Finish: finish, Usage: c.usage,
	}
	if finish != llm.FinishStop {
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
