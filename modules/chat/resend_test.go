package chat

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	mediasvc "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// resenderProfile is an agent that reads descriptions and has resend_media, not
// examine_media.
func resenderProfile(name string) ProfileConfig {
	p := examiner(name)
	p.Media.Examine, p.Media.Resend = nil, true
	return p
}

// video is a video message part named name, which the fake describer keys
// on.
func video(name string) otogi.Part {
	return otogi.Part{ID: otogi.PartID(name), Kind: otogi.PartMedia, Media: &otogi.Media{Role: otogi.MediaVideo,
		Source: otogi.MediaSource{Kind: otogi.MediaSourceOpaque, Locator: otogi.ProviderLocator{Data: []byte(name)}}}}
}

// resends is the model saying text, if any, and calling resend_media with
// each id.
func resends(text string, ids ...string) llm.Response {
	r := llm.Response{Turn: llm.Turn{Role: llm.RoleModel}, Finish: llm.FinishToolCalls}
	if text != "" {
		r.Turn.Content = otogi.TextMessage(text)
	}
	for i, id := range ids {
		args, _ := json.Marshal(map[string]string{"id": id})
		r.Turn.Calls = append(r.Turn.Calls, llm.Call{ID: fmt.Sprintf("r%d", i), Name: resendTool, Args: args})
	}
	return r
}

// An agent with resend_media sees IDs and is offered the tool, without
// examine_media. The picture it names follows its words as a message of
// its own, quoting as the reply does, from the media module's copy or else
// fetched again, and chat.reply records it.
func TestResend(t *testing.T) {
	for _, kept := range []bool{true, false} {
		h, media := newExamineHarness(t, resenderProfile("otogi"))
		media.originals = map[string]bool{"tank": kept}
		p := h.llm["otogi"]
		p.responses = []llm.Response{resends("Here it is.", "#00a0a6")}
		h.mustHandle(h.message("Alice", "", withParts("", photo("tank"))))
		ask := h.message("Bob", "otogi send that tank again")
		h.mustHandle(ask)
		req := p.requests[0]
		if !strings.Contains(lastUserText(req), "[photo #00a0a6: a tank]") || !offers(req, resendTool) || offers(req, examineTool) {
			t.Fatalf("kept %v: turn %q, tools %+v", kept, lastUserText(req), req.Tools)
		}
		if len(p.requests) != 1 || h.streams[0].final.PlainText() != "Here it is." {
			t.Fatalf("kept %v: requests %d, reply %q", kept, len(p.requests), h.streams[0].final.PlainText())
		}
		want := "kept bytes of tank"
		if !kept {
			want = "bytes of tank"
		}
		if len(h.sends) != 1 {
			t.Fatalf("kept %v: sends %+v", kept, h.sends)
		}
		s := h.sends[0]
		if s.slot != pictureSlot || len(s.msg.Parts) != 1 || s.msg.Parts[0].Media == nil || s.msg.Parts[0].Media.Role != otogi.MediaImage ||
			string(s.msg.Parts[0].Media.Source.Inline) != want || len(s.msg.Relations) != 1 || s.msg.Relations[0].Target.SourceEvent != ask.SourceID {
			t.Fatalf("kept %v: picture message %+v", kept, s)
		}
		r, payload := lastReplyRecord(t, h)
		if resent, ok := payload["resent"].(*sentPicture); r.Outcome != "ok" || !ok || resent.ID != "00a0a6" || resent.Ref == "" {
			t.Fatalf("kept %v: record %v %+v", kept, r.Outcome, payload)
		}
	}
}

// One picture or sticker per reply: a second, a picture or a sticker, in
// the same round or later, answers sent false, no error, so the reply ends
// without asking the model again, and only the first is sent.
func TestOnePictureOrStickerPerReply(t *testing.T) {
	p := resenderProfile("otogi")
	p.Media.Stickers = []int{1, 3}
	for _, c := range []struct {
		name  string
		resp  llm.Response
		slot  string
		calls int
	}{
		{"two pictures", resends("", "#00a0a6", "#00a0a6"), pictureSlot, 2},
		{"a picture, then a sticker", func() llm.Response {
			r := resends("", "#00a0a6")
			r.Turn.Calls = append(r.Turn.Calls, picks("", 1).Turn.Calls...)
			return r
		}(), pictureSlot, 2},
		{"a sticker, then a picture", func() llm.Response {
			r := picks("", 1)
			r.Turn.Calls = append(r.Turn.Calls, resends("", "#00a0a6").Turn.Calls...)
			return r
		}(), stickerSlot, 2},
	} {
		h, _ := newExamineHarness(t, p)
		h.llm["otogi"].responses = []llm.Response{c.resp}
		h.mustHandle(h.message("Alice", "", withParts("", photo("tank"))))
		h.mustHandle(h.message("Bob", "otogi both please"))
		if n := len(h.llm["otogi"].requests); n != 1 {
			t.Fatalf("%s: the model was asked %d times", c.name, n)
		}
		if len(h.sends) != 1 || h.sends[0].slot != c.slot {
			t.Fatalf("%s: sends %+v", c.name, h.sends)
		}
		rec := toolRecords(h)
		if len(rec) != c.calls || rec[1]["error"] != nil || string(rec[1]["result"].(json.RawMessage)) != `{"reason":"one picture or sticker a reply","sent":false}` {
			t.Fatalf("%s: chat.tool %+v", c.name, rec)
		}
	}
}

// When a picture cannot be sent again, the agent is told why and what to
// do instead, and the reply goes on in words, with nothing sent.
func TestResendFailures(t *testing.T) {
	for _, c := range []struct {
		name  string
		id    string
		setup func(*harness, *describer)
		want  string
	}{
		{"not an ID", "00a0a6", nil, `"00a0a6": want the ID as the chat shows it, as "#2ce7ad"`},
		{"a kept sticker", "sticker 3", nil, "that is a kept sticker: send it with send_sticker"},
		{"not shown", "#ffffff", nil, "no picture #ffffff in this chat's recent messages"},
		{"a video", "#0c1d00", nil, "videos cannot be sent again"},
		{"not fetched", "#00a0a6", func(h *harness, _ *describer) { h.fetchErr = errors.New("404") }, "its original can no longer be fetched; say so in words"},
	} {
		h, media := newExamineHarness(t, resenderProfile("otogi"))
		media.kept["clip"] = mediasvc.Description{Kind: mediasvc.KindVideo, Summary: "a tank driving", ID: "0c1d00"}
		if c.setup != nil {
			c.setup(h, media)
		}
		h.llm["otogi"].responses = []llm.Response{resends("", c.id), answer("I can't send it.")}
		h.mustHandle(h.message("Alice", "", withParts("", photo("tank"), video("clip"))))
		h.mustHandle(h.message("Bob", "otogi send it again"))
		if rec := toolRecords(h); len(rec) != 1 || rec[0]["error"] != c.want {
			t.Fatalf("%s: chat.tool %+v", c.name, rec)
		}
		if len(h.sends) != 0 || h.streams[len(h.streams)-1].final.PlainText() != "I can't send it." {
			t.Fatalf("%s: sends %+v, reply %q", c.name, h.sends, h.streams[len(h.streams)-1].final.PlainText())
		}
		if _, payload := lastReplyRecord(t, h); payload["resent"] != nil {
			t.Fatalf("%s: a reply that sent nothing recorded %+v", c.name, payload["resent"])
		}
	}
}

// A picture the platform refuses makes the reply an error, recorded with
// the picture and no ref; the words went out already.
func TestResendRefused(t *testing.T) {
	h, media := newExamineHarness(t, resenderProfile("otogi"))
	media.originals = map[string]bool{"tank": true}
	h.sendErr = errors.New("upload refused")
	h.llm["otogi"].responses = []llm.Response{resends("Here.", "#00a0a6")}
	h.mustHandle(h.message("Alice", "", withParts("", photo("tank"))))
	if err := h.handle(h.message("Bob", "otogi again")); err == nil || !strings.Contains(err.Error(), "upload refused") {
		t.Fatalf("err %v", err)
	}
	r, payload := lastReplyRecord(t, h)
	if resent, ok := payload["resent"].(*sentPicture); r.Outcome != "error" || !ok || resent.ID != "00a0a6" || resent.Ref != "" {
		t.Fatalf("record %v %+v", r.Outcome, payload)
	}
	if h.streams[0].final.PlainText() != "Here." {
		t.Fatalf("reply %q", h.streams[0].final.PlainText())
	}
}
