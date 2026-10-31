package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// script is a provider answering each Generate with the next response,
// streaming its text as one partial, and keeping the requests.
type script struct {
	responses []Response
	requests  []Request
}

func (s *script) Generate(_ context.Context, req Request, partial func(otogi.Message) error) (Response, error) {
	s.requests = append(s.requests, req)
	resp := s.responses[min(len(s.requests), len(s.responses))-1]
	if partial != nil && len(resp.Turn.Content.Parts) > 0 {
		if err := partial(resp.Turn.Content); err != nil {
			return Response{}, err
		}
	}
	return resp, nil
}

func say(text string, calls ...Call) Response {
	r := Response{Turn: Turn{Role: RoleModel, Calls: calls}, Usage: Usage{Input: 10, Output: 1}}
	if text != "" {
		r.Turn.Content = otogi.TextMessage(text)
	}
	if len(calls) > 0 {
		r.Finish = FinishToolCalls
	}
	return r
}

type toolFunc func(args json.RawMessage) (any, error)

func (f toolFunc) Function() Function {
	return Function{Name: "add", Description: "adds a and b", Parameters: json.RawMessage(`{"type":"object"}`)}
}
func (f toolFunc) Call(_ context.Context, _ otogi.Event, args json.RawMessage) (any, error) {
	return f(args)
}

var add = toolFunc(func(args json.RawMessage) (any, error) {
	var in struct{ A, B int }
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	return map[string]int{"sum": in.A + in.B}, nil
})

func TestRunRunsCallsUntilTheModelAnswers(t *testing.T) {
	t.Parallel()
	p := &script{responses: []Response{
		say("Checking.", Call{ID: "c1", Name: "add", Args: json.RawMessage(`{"a":1,"b":2}`)}),
		say("It is 3."),
	}}
	var partials []string
	var calls []CallRecord
	out, err := Run(context.Background(), p, Request{Model: "m", Tools: []ToolSpec{{Builtin: BuiltinGoogleSearch}}}, map[string]Tool{"add": add}, otogi.Event{}, 4,
		func(m otogi.Message) error { partials = append(partials, m.PlainText()); return nil },
		func(c CallRecord) { calls = append(calls, c) })
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Reply.PlainText(); got != "Checking.\n\nIt is 3." {
		t.Errorf("reply %q", got)
	}
	if strings.Join(partials, "|") != "Checking.|Checking.\n\nIt is 3." {
		t.Errorf("partials %q", partials)
	}
	if len(out.Turns) != 3 || out.Turns[1].Role != RoleTool || string(out.Turns[1].Results[0].Output) != `{"sum":3}` || out.Usage.Input != 20 {
		t.Fatalf("turns %+v usage %+v", out.Turns, out.Usage)
	}
	second := p.requests[1]
	if len(second.Turns) != 2 || len(second.Tools) != 2 || second.Tools[0].Builtin != BuiltinGoogleSearch || second.Tools[1].Function.Name != "add" {
		t.Fatalf("second request %+v", second)
	}
	if len(calls) != 1 || calls[0].Result.Error != "" {
		t.Fatalf("calls %+v", calls)
	}
}

func TestRunAnswersFailingCallsWithErrors(t *testing.T) {
	t.Parallel()
	failing := toolFunc(func(json.RawMessage) (any, error) { return nil, errors.New("no such chat") })
	panicking := toolFunc(func(json.RawMessage) (any, error) { panic("boom") })
	p := &script{responses: []Response{
		say("", Call{ID: "1", Name: "missing"}, Call{ID: "2", Name: "fail"}, Call{ID: "3", Name: "panic"}),
		say("Sorry."),
	}}
	var calls []CallRecord
	out, err := Run(context.Background(), p, Request{}, map[string]Tool{"fail": failing, "panic": panicking}, otogi.Event{}, 4, nil, func(c CallRecord) { calls = append(calls, c) })
	if err != nil {
		t.Fatal(err)
	}
	results := out.Turns[1].Results
	if !strings.Contains(results[0].Error, `no function "missing"`) || results[1].Error != "no such chat" || !strings.Contains(results[2].Error, "boom") || strings.Contains(results[2].Error, "goroutine") {
		t.Fatalf("results %+v", results)
	}
	if calls[2].Panic == "" || !strings.Contains(calls[2].Panic, "goroutine") {
		t.Fatalf("panic not recorded: %+v", calls[2])
	}
}

func TestRunLimitsRounds(t *testing.T) {
	t.Parallel()
	runs := 0
	counting := toolFunc(func(json.RawMessage) (any, error) { runs++; return "ok", nil })
	p := &script{responses: []Response{say("", Call{ID: "c", Name: "add"})}}
	out, err := Run(context.Background(), p, Request{}, map[string]Tool{"add": counting}, otogi.Event{}, 2, nil, nil)
	if !errors.Is(err, otogi.ErrUnavailable) || runs != 2 || len(p.requests) != 4 {
		t.Fatalf("err %v, runs %d, requests %d", err, runs, len(p.requests))
	}
	if last := out.Turns[len(out.Turns)-2]; !strings.Contains(last.Results[0].Error, "too many") {
		t.Fatalf("over-limit calls answered %+v", last.Results)
	}
}

func TestRunStopsWhenPartialFails(t *testing.T) {
	t.Parallel()
	p := &script{responses: []Response{say("hi")}}
	gone := errors.New("gone")
	if _, err := Run(context.Background(), p, Request{}, nil, otogi.Event{}, 1, func(otogi.Message) error { return gone }, nil); !errors.Is(err, gone) {
		t.Fatalf("err %v", err)
	}
}

func TestJoinMergesAdjacentText(t *testing.T) {
	t.Parallel()
	a := otogi.TextMessage("one")
	a.Parts[0].Annotations = []otogi.Annotation{{Offset: 0, Length: 3, Kind: otogi.AnnotationBold}}
	a.Relations = []otogi.Relation{{Kind: otogi.RelationReply}}
	b := otogi.TextMessage("two")
	b.Parts[0].Annotations = []otogi.Annotation{{Offset: 0, Length: 3, Kind: otogi.AnnotationItalic}}
	image := otogi.Part{ID: "i", Kind: otogi.PartMedia, Media: &otogi.Media{Role: otogi.MediaImage}}
	b.Parts, b.Roots = append(b.Parts, image), append(b.Roots, "i")
	got := Join(a, b)
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(got.Parts) != 2 || got.Parts[0].Text != "one\n\ntwo" || got.Parts[0].Annotations[1].Offset != 5 || got.Parts[1].Kind != otogi.PartMedia || len(got.Relations) != 1 {
		t.Fatalf("joined %+v", got)
	}
	if a.Parts[0].Text != "one" || len(a.Parts[0].Annotations) != 1 {
		t.Fatal("Join changed its input")
	}
}
