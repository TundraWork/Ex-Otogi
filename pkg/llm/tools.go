package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Built-in tools a provider may run itself. A provider that has no such
// tool rejects the request with otogi.ErrUnsupported.
const (
	// BuiltinGoogleSearch grounds replies in Google Search results.
	BuiltinGoogleSearch = "google_search"
	// BuiltinURLContext lets the model read pages the conversation links.
	BuiltinURLContext = "url_context"
	// BuiltinCodeExecution lets the model run code it writes.
	BuiltinCodeExecution = "code_execution"
)

// ToolSpec offers the model one tool: a provider built-in or a function.
type ToolSpec struct {
	// Builtin names a built-in tool, such as BuiltinGoogleSearch.
	Builtin string `json:"builtin,omitempty"`
	// Function declares a function the caller runs.
	Function *Function `json:"function,omitempty"`
}

// Function declares a function a model may call.
type Function struct {
	// Name identifies the function; letters, digits, and underscores.
	Name string `json:"name"`
	// Description tells the model what the function does and when to use
	// it.
	Description string `json:"description"`
	// Parameters is the JSON Schema of the arguments object.
	Parameters json.RawMessage `json:"parameters,omitempty"`
}

// Call is a function call a model made.
type Call struct {
	// ID pairs the call with its result.
	ID string `json:"id"`
	// Name is the function's name.
	Name string `json:"name"`
	// Args is the arguments object, as JSON.
	Args json.RawMessage `json:"args"`
}

// Result answers a call.
type Result struct {
	// CallID is the call's ID.
	CallID string `json:"call_id"`
	// Name is the function's name.
	Name string `json:"name"`
	// Output is the function's result, as JSON; empty when it failed.
	Output json.RawMessage `json:"output,omitempty"`
	// Error says why the call failed.
	Error string `json:"error,omitempty"`
}

// Tool is a function a model may call, implemented by a module. Modules
// offer tools as the service "llm.tools/<module id>", a []Tool.
type Tool interface {
	// Function declares the tool to the model.
	Function() Function
	// Call runs the function for a model answering event. The tool applies
	// the same rules as the module's commands; for example, a chat's notes
	// belong to the event's chat. The result is encoded as JSON for the
	// model; an error is shown to the model, which can explain or try
	// again.
	Call(ctx context.Context, event otogi.Event, args json.RawMessage) (any, error)
}

// ToolsService names the service through which a module offers its tools.
func ToolsService(module otogi.ModuleID) string { return "llm.tools/" + string(module) }

// CallRecord is one function call Run made, for the caller to record.
type CallRecord struct {
	Call     Call
	Result   Result
	Duration time.Duration
	// Panic is the stack of a tool that panicked.
	Panic string
}

// RunResult is the outcome of Run.
type RunResult struct {
	// Turns are the turns Run added, in order: model turns and, between
	// them, the RoleTool turns answering their calls. Keep them to continue
	// the conversation.
	Turns []Turn
	// Reply is everything the model showed across all rounds, as one
	// message.
	Reply otogi.Message
	// Final is the last model response.
	Final Response
	// Usage sums all rounds.
	Usage Usage
	// Sources are the pages cited in any round.
	Sources []Source
}

// Run answers req with p, running the function calls the model makes with
// tools, until the model answers without calling any. It offers the tools'
// functions alongside req.Tools. partial, if not nil, sees the reply so far
// across rounds; onCall, if not nil, sees each call as it finishes.
//
// A call to an unknown function, or one that fails or panics, is answered
// with an error the model sees. Calls are run for at most maxRounds rounds;
// the next round's calls are answered with an error asking the model to
// answer with what it has, and a model that still calls functions then is
// an error.
func Run(ctx context.Context, p Provider, req Request, tools map[string]Tool, event otogi.Event,
	maxRounds int, partial func(otogi.Message) error, onCall func(CallRecord)) (RunResult, error) {
	for _, t := range tools {
		f := t.Function()
		req.Tools = append(req.Tools, ToolSpec{Function: &f})
	}
	var out RunResult
	for round := 1; ; round++ {
		var show func(otogi.Message) error
		if partial != nil {
			shown := out.Reply
			show = func(m otogi.Message) error { return partial(Join(shown, m)) }
		}
		resp, err := p.Generate(ctx, req, show)
		if err != nil {
			return out, err
		}
		out.Final, out.Usage = resp, out.Usage.Add(resp.Usage)
		out.Turns = append(out.Turns, resp.Turn)
		out.Reply = Join(out.Reply, resp.Turn.Content)
		out.Sources = append(out.Sources, resp.Sources...)
		req.Turns = append(req.Turns, resp.Turn)
		if len(resp.Turn.Calls) == 0 {
			return out, nil
		}
		if round > maxRounds+1 {
			return out, fmt.Errorf("the model still called functions after %d rounds: %w", maxRounds, otogi.ErrUnavailable)
		}
		results := make([]Result, len(resp.Turn.Calls))
		for i, c := range resp.Turn.Calls {
			if round > maxRounds {
				results[i] = Result{CallID: c.ID, Name: c.Name, Error: "too many function calls in this reply; answer with what you have"}
				continue
			}
			started := time.Now()
			var stack string
			results[i], stack = runCall(ctx, tools[c.Name], c, event)
			if onCall != nil {
				onCall(CallRecord{Call: c, Result: results[i], Duration: time.Since(started), Panic: stack})
			}
		}
		toolTurn := Turn{Role: RoleTool, Results: results}
		out.Turns = append(out.Turns, toolTurn)
		req.Turns = append(req.Turns, toolTurn)
	}
}

// runCall runs one call, turning a failure or a panic into an error result.
// stack is set when the tool panicked.
func runCall(ctx context.Context, tool Tool, c Call, event otogi.Event) (result Result, stack string) {
	result = Result{CallID: c.ID, Name: c.Name}
	if tool == nil {
		result.Error = fmt.Sprintf("there is no function %q", c.Name)
		return result, ""
	}
	defer func() {
		if r := recover(); r != nil {
			result.Output, result.Error = nil, fmt.Sprintf("the function failed unexpectedly: %v", r)
			stack = string(debug.Stack())
		}
	}()
	out, err := tool.Call(ctx, event, c.Args)
	if err != nil {
		result.Error = err.Error()
		return result, ""
	}
	data, err := json.Marshal(out)
	if err != nil {
		result.Error = fmt.Sprintf("encode the result: %v", err)
		return result, ""
	}
	result.Output = data
	return result, ""
}

// Join appends b to a as one message: adjacent text parts merge, separated
// by a blank line, and part IDs are renumbered.
func Join(a, b otogi.Message) otogi.Message {
	var out otogi.Message
	add := func(p otogi.Part) {
		p.ID = otogi.PartID("p" + strconv.Itoa(len(out.Parts)))
		out.Parts = append(out.Parts, p)
		out.Roots = append(out.Roots, p.ID)
	}
	for _, p := range a.Parts {
		add(p)
	}
	for i, p := range b.Parts {
		if n := len(out.Parts); i == 0 && n > 0 && p.Kind == otogi.PartText && out.Parts[n-1].Kind == otogi.PartText {
			last := &out.Parts[n-1]
			base := len(last.Text) + 2
			last.Text += "\n\n" + p.Text
			last.Annotations = append([]otogi.Annotation(nil), last.Annotations...)
			for _, an := range p.Annotations {
				an.Offset += base
				last.Annotations = append(last.Annotations, an)
			}
			continue
		}
		add(p)
	}
	out.Relations = a.Relations
	return out
}

// NewTool makes a Tool from its declaration and a function.
func NewTool(f Function, call func(ctx context.Context, event otogi.Event, args json.RawMessage) (any, error)) Tool {
	return funcTool{f: f, call: call}
}

type funcTool struct {
	f    Function
	call func(context.Context, otogi.Event, json.RawMessage) (any, error)
}

func (t funcTool) Function() Function { return t.f }

func (t funcTool) Call(ctx context.Context, event otogi.Event, args json.RawMessage) (any, error) {
	return t.call(ctx, event, args)
}

type agentKey struct{}

// ContextWithAgent returns ctx naming the agent (for example a chat
// profile) whose model runs tools in it, so a tool can attribute what it
// writes.
func ContextWithAgent(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, agentKey{}, name)
}

// AgentFrom returns the agent ctx names, or "".
func AgentFrom(ctx context.Context) string {
	name, _ := ctx.Value(agentKey{}).(string)
	return name
}
