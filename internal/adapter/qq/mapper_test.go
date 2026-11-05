package qq

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	qqstore "github.com/TundraWork/Ex-Otogi/internal/adapter/qq/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func testMapper(t *testing.T) *mapper {
	t.Helper()
	s, err := qqstore.Open(context.Background(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &mapper{account: "bot", refs: s, self: func() string { return "B1" }}
}

func mapOne(t *testing.T, m *mapper, typ, d string) otogi.Event {
	t.Helper()
	e, err := m.mapFrame(context.Background(), frame{Op: opDispatch, T: typ, ID: typ + ":x", D: json.RawMessage(d)}, time.Unix(100, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	e.ID = "e"
	if err := e.Validate(); err != nil {
		t.Fatalf("invalid event %+v: %v", e, err)
	}
	return e
}

func TestMapGroupMessages(t *testing.T) {
	m := testMapper(t)

	// In full mode an @ to the bot arrives as a group message, its mention
	// flagged is_you; other mentions keep the member's name.
	e := mapOne(t, m, "GROUP_MESSAGE_CREATE", `{"id":"g1","content":"<@B9> /status <@M2> and <@all>","group_openid":"G1",
		"author":{"id":"a","member_openid":"M1","username":"Alice"},
		"mentions":[{"member_openid":"B9","username":"Otogi","is_you":true},{"member_openid":"M2","username":"Bob"}],
		"message_scene":{"ext":["msg_idx=REFIDX_g1"]}}`)
	if e.Conversation.ID != "group:G1" || e.Conversation.Kind != otogi.ConversationGroup || e.Actor.ID != "user:M1" || !e.Mentioned || e.Direction != otogi.DirectionIncoming {
		t.Fatalf("event = %+v", e)
	}
	p := e.Message.Parts[0]
	if p.Text != "@Otogi /status @Bob and <@all>" {
		t.Fatalf("text = %q", p.Text)
	}
	want := []otogi.Annotation{{Offset: 0, Length: 6, Kind: otogi.AnnotationMention, Target: "user:B1"}, {Offset: 15, Length: 4, Kind: otogi.AnnotationMention, Target: "user:M2"}}
	if !reflect.DeepEqual(p.Annotations, want) {
		t.Fatalf("annotations = %+v", p.Annotations)
	}

	// In @-only mode QQ strips the @ and leaves a space.
	if e := mapOne(t, m, "GROUP_AT_MESSAGE_CREATE", `{"id":"g2","content":" /whoami","group_openid":"G1","author":{"member_openid":"M1"}}`); !e.Mentioned || e.Message.PlainText() != "/whoami" {
		t.Fatalf("at event = %+v", e)
	}

	// The bot's own message, echoed back.
	if e := mapOne(t, m, "GROUP_MESSAGE_CREATE", `{"id":"g3","content":"pong","group_openid":"G1","author":{"id":"B1","bot":true,"member_openid":"B9"}}`); e.Direction != otogi.DirectionOutgoing {
		t.Fatalf("echo direction = %s", e.Direction)
	}

	// A quote names the quoted message by its REFIDX.
	e = mapOne(t, m, "GROUP_MESSAGE_CREATE", `{"id":"g4","content":"agreed","group_openid":"G1","author":{"member_openid":"M2"},"message_type":103,
		"message_scene":{"ext":["msg_idx=REFIDX_g4","ref_msg_idx=REFIDX_g1"]},"msg_elements":[{"content":"quoted"}]}`)
	if len(e.Message.Relations) != 1 || e.Message.Relations[0].Kind != otogi.RelationReply || e.Message.Relations[0].Target.SourceEvent != "g1" || e.Message.Relations[0].Target.Conversation != "group:G1" {
		t.Fatalf("relations = %+v", e.Message.Relations)
	}
	// One it never saw keeps the REFIDX.
	e = mapOne(t, m, "GROUP_MESSAGE_CREATE", `{"id":"g5","content":"old","group_openid":"G1","author":{"member_openid":"M2"},"message_type":103,"message_scene":{"ext":["ref_msg_idx=REFIDX_old"]}}`)
	if e.Message.Relations[0].Target.SourceEvent != "REFIDX_old" {
		t.Fatalf("unknown quote = %+v", e.Message.Relations)
	}
}

func TestMapAttachments(t *testing.T) {
	m := testMapper(t)
	e := mapOne(t, m, "C2C_MESSAGE_CREATE", `{"id":"c1","content":"","author":{"user_openid":"U1"},"attachments":[
		{"url":"https://multimedia.nt.qq.com.cn/download?fileid=1","filename":"a.png","size":1234,"width":10,"height":20,"content_type":"image/png"},
		{"url":"//multimedia.nt.qq.com.cn/download?fileid=2","filename":"v.amr","content_type":"voice","voice_wav_url":"https://w/v.wav","asr_refer_text":"你好"},
		{"url":"https://f/doc.pdf","filename":"doc.pdf","size":9,"content_type":"file"}]}`)
	parts := e.Message.Parts
	if len(parts) != 3 || len(e.Message.Roots) != 3 {
		t.Fatalf("parts = %+v", parts)
	}
	if img := parts[0].Media; parts[0].Kind != otogi.PartMedia || img.Role != otogi.MediaImage || img.MediaType != "image/png" || img.ByteSize != 1234 || img.Width != 10 || img.Source.Kind != otogi.MediaSourceExternal {
		t.Fatalf("image = %+v", img)
	}
	if voice := parts[1].Media; voice.Role != otogi.MediaVoice || voice.Source.URL != "https://multimedia.nt.qq.com.cn/download?fileid=2" || len(voice.Extensions) != 1 {
		t.Fatalf("voice = %+v", voice)
	}
	if parts[2].Kind != otogi.PartFile || parts[2].Media.Filename != "doc.pdf" {
		t.Fatalf("file = %+v", parts[2])
	}
}

func TestMapServiceEvents(t *testing.T) {
	m := testMapper(t)
	e := mapOne(t, m, "GROUP_ADD_ROBOT", `{"timestamp":1730000000,"group_openid":"G1","op_member_openid":"M1"}`)
	if e.Action != otogi.ActionMessageService || e.Conversation.ID != "group:G1" || e.Actor.ID != "user:M1" || e.Message.Parts[0].Opaque.Name != "GROUP_ADD_ROBOT" {
		t.Fatalf("event = %+v", e)
	}
	if e := mapOne(t, m, "FRIEND_ADD", `{"openid":"U1"}`); e.Conversation.ID != "user:U1" || e.Conversation.Kind != otogi.ConversationDirect {
		t.Fatalf("friend = %+v", e)
	}
	if e := mapOne(t, m, "SOMETHING_NEW", `{"x":1}`); e.Conversation.ID != "bot" || string(e.Message.Parts[0].Opaque.Data) != `{"x":1}` {
		t.Fatalf("unknown = %+v", e)
	}
}
