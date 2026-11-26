package qq

import (
	"context"
	"encoding/hex"
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
	return &mapper{account: "bot", refs: s, names: s, self: func() string { return "B1" }}
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
	// flagged is_you; other mentions keep the member's name. Members are named
	// by their group card, or by their QQ nickname without one.
	e := mapOne(t, m, "GROUP_MESSAGE_CREATE", `{"id":"g1","content":"<@B9> /status <@M2> and <@all>","group_openid":"G1",
		"author":{"id":"a","member_openid":"M1","username":"alice_qq","member_nickname":"Alice"},
		"mentions":[{"member_openid":"B9","username":"Otogi","is_you":true},{"member_openid":"M2","username":"bob_qq","member_nickname":"Bob"}],
		"message_scene":{"ext":["msg_idx=REFIDX_g1"]}}`)
	if e.Conversation.ID != "group:G1" || e.Conversation.Kind != otogi.ConversationGroup || e.Actor.ID != "user:M1" || e.Actor.Name != "Alice" || !e.Mentioned || e.Direction != otogi.DirectionIncoming {
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
	// Another bot's message comes in, marked as a bot's.
	if e := mapOne(t, m, "GROUP_MESSAGE_CREATE", `{"id":"g6","content":"hi","group_openid":"G1","author":{"id":"B2","bot":true,"member_openid":"B8"}}`); e.Direction != otogi.DirectionIncoming || !e.Actor.Bot {
		t.Fatalf("other bot = %s %+v", e.Direction, e.Actor)
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
		{"url":"//multimedia.nt.qq.com.cn/download?fileid=2","filename":"v.amr","content_type":"voice","voice_wav_url":"https://w/v.wav","asr_refer_text":" 你好 "},
		{"url":"https://f/doc.pdf","filename":"doc.pdf","size":9,"content_type":"file"},
		{"url":"https://multimedia.nt.qq.com.cn/download?fileid=3","filename":"w.amr","content_type":"voice"}]}`)
	parts := e.Message.Parts
	if len(parts) != 4 || len(e.Message.Roots) != 4 {
		t.Fatalf("parts = %+v", parts)
	}
	if img := parts[0].Media; parts[0].Kind != otogi.PartMedia || img.Role != otogi.MediaImage || img.MediaType != "image/png" || img.ByteSize != 1234 || img.Width != 10 || img.Source.Kind != otogi.MediaSourceExternal {
		t.Fatalf("image = %+v", img)
	}
	if voice := parts[1].Media; voice.Role != otogi.MediaVoice || voice.Source.URL != "https://multimedia.nt.qq.com.cn/download?fileid=2" || len(voice.Extensions) != 1 || voice.Transcript != "你好" {
		t.Fatalf("voice = %+v", voice)
	}
	if parts[2].Kind != otogi.PartFile || parts[2].Media.Filename != "doc.pdf" {
		t.Fatalf("file = %+v", parts[2])
	}
	if voice := parts[3].Media; voice.Role != otogi.MediaVoice || voice.Transcript != "" {
		t.Fatalf("voice without a transcript = %+v", voice)
	}
}

// An image named by 32 hex digits carries them as its MD5, in either case
// and whatever the extension says; other names, videos, and files carry
// none.
func TestMapImageMD5(t *testing.T) {
	m := testMapper(t)
	e := mapOne(t, m, "C2C_MESSAGE_CREATE", `{"id":"c1","content":"","author":{"user_openid":"U1"},"attachments":[
		{"url":"https://q/1","filename":"5A5CB11263AA9345A028A296BDCD600E.jpg","content_type":"image/gif"},
		{"url":"https://q/2","filename":"819d99a1b2c3d4e5f60718293a4b5c6d.jpg","content_type":"image/png"},
		{"url":"https://q/3","filename":"a.png","content_type":"image/png"},
		{"url":"https://q/4","filename":"5A5CB11263AA9345A028A296BDCD600Z.png","content_type":"image/png"},
		{"url":"https://q/5","filename":"6a790e8880cdb73a883591b101eb3b78.mp4","content_type":"video/mp4"},
		{"url":"https://q/6","filename":"6a790e8880cdb73a883591b101eb3b78.pdf","content_type":"file"}]}`)
	want := []string{"5a5cb11263aa9345a028a296bdcd600e", "819d99a1b2c3d4e5f60718293a4b5c6d", "", "", "", ""}
	for i, p := range e.Message.Parts {
		got := ""
		if hs := p.Media.Hashes; len(hs) == 1 && hs[0].Algorithm == "md5" {
			got = hex.EncodeToString(hs[0].Value)
		} else if len(hs) != 0 {
			t.Fatalf("part %d hashes = %+v", i, hs)
		}
		if got != want[i] {
			t.Errorf("part %d md5 = %q, want %q", i, got, want[i])
		}
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

func TestMapSentMessage(t *testing.T) {
	m := testMapper(t)
	e := mapOne(t, m, sentType, `{"id":"s1","content":"**hi**","timestamp":"2026-11-06T02:00:00Z","peer":"U1","self":"B1","self_name":"Otogi"}`)
	if e.Direction != otogi.DirectionOutgoing || e.Conversation.ID != "user:U1" || e.Actor.ID != "user:B1" || e.Actor.Name != "Otogi" || e.Message.PlainText() != "**hi**" || e.SourceID != "s1" {
		t.Fatalf("event = %+v", e)
	}
	e = mapOne(t, m, sentType, `{"id":"s2","content":"hello","timestamp":"2026-11-06T02:00:00Z","group":"G1","self":"B1","self_name":"Otogi"}`)
	if e.Direction != otogi.DirectionOutgoing || e.Conversation.ID != "group:G1" || e.Conversation.Kind != otogi.ConversationGroup || e.Actor.ID != "user:B1" || e.Actor.Bot || e.SourceID != "s2" {
		t.Fatalf("group event = %+v", e)
	}
	if _, err := m.mapFrame(context.Background(), frame{Op: opDispatch, T: sentType, D: json.RawMessage(`{"id":"s3","content":"?","self":"B1"}`)}, time.Now()); err == nil {
		t.Fatal("a sent message with no chat was mapped")
	}
}

// Names a message shows are kept: a later mention that QQ sends without a
// name still reads as the person.
func TestMapLearnsNames(t *testing.T) {
	m := testMapper(t)
	mapOne(t, m, "GROUP_MESSAGE_CREATE", `{"id":"g1","content":"hi <@M2>","group_openid":"G1",
		"author":{"member_openid":"M1","username":"Alice"},"mentions":[{"member_openid":"M2","username":"Bob"}]}`)
	e := mapOne(t, m, "GROUP_MESSAGE_CREATE", `{"id":"g2","content":"<@M1> hello","group_openid":"G1",
		"author":{"member_openid":"M2"},"mentions":[{"member_openid":"M1"}]}`)
	if e.Message.PlainText() != "@Alice hello" {
		t.Fatalf("text = %q", e.Message.PlainText())
	}
	for peer, want := range map[string]string{"user:M1": "Alice", "user:M2": "Bob"} {
		if p, err := m.names.Peer(context.Background(), peer); err != nil || p.Name != want {
			t.Fatalf("%s = %+v, %v", peer, p, err)
		}
	}

	// A group card names the member in that group's message only: the open
	// ID is the same in every group, so the kept name stays the QQ nickname.
	e = mapOne(t, m, "GROUP_MESSAGE_CREATE", `{"id":"g3","content":"hi","group_openid":"G2",
		"author":{"member_openid":"M1","username":"Alice","member_nickname":"Alice in G2"}}`)
	if e.Actor.Name != "Alice in G2" {
		t.Fatalf("actor = %+v", e.Actor)
	}
	if p, err := m.names.Peer(context.Background(), "user:M1"); err != nil || p.Name != "Alice" {
		t.Fatalf("user:M1 = %+v, %v", p, err)
	}
}

// QQ faces in content become the names they carry, marked as emoji; a
// sticker's face goes, and its picture, which comes as an attachment, is a
// sticker.
func TestMapFaces(t *testing.T) {
	m := testMapper(t)
	// A market face (type 4), a store sticker, has a name and no ID, and is a
	// sticker sent without its picture; a built-in face (type 3) has both and
	// is an emoji; a face whose name cannot be read is [表情].
	e := mapOne(t, m, "GROUP_MESSAGE_CREATE", `{"id":"g1","group_openid":"G1","author":{"member_openid":"M1"},
		"content":"冲<faceType=4,faceId=\"\",ext=\"eyJ0ZXh0IjoiW+i/m+aUu10ifQ==\">！<faceType=3,faceId=\"5\",ext=\"eyJ0ZXh0IjoiW+a1geazql0ifQ==\"><faceType=3,faceId=\"9\",ext=\"!\">"}`)
	p := e.Message.Parts[0]
	if p.Text != "冲[进攻]！[流泪][表情]" {
		t.Fatalf("text = %q", p.Text)
	}
	want := []otogi.Annotation{
		{Offset: 3, Length: 8, Kind: otogi.AnnotationSticker, Target: "qq:face:4"},
		{Offset: 14, Length: 8, Kind: otogi.AnnotationEmoji, Target: "qq:face:3:5"},
		{Offset: 22, Length: 8, Kind: otogi.AnnotationEmoji, Target: "qq:face:3:9"},
	}
	if !reflect.DeepEqual(p.Annotations, want) {
		t.Fatalf("annotations = %+v", p.Annotations)
	}
	e = mapOne(t, m, "GROUP_MESSAGE_CREATE", `{"id":"g2","group_openid":"G1","author":{"member_openid":"M1"},
		"content":"<faceType=6,faceId=\"0\",ext=\"eyJ0ZXh0IjoiIn0=\">",
		"attachments":[{"content_type":"image/jpeg","url":"https://multimedia.nt.qq.com.cn/x","width":806,"height":806}]}`)
	if len(e.Message.Parts) != 1 || e.Message.Parts[0].Media == nil || e.Message.Parts[0].Media.Role != otogi.MediaSticker {
		t.Fatalf("parts = %+v", e.Message.Parts)
	}
}

// A group message carries its sender's standing: owner and admin as roles,
// an ordinary member or an unknown value as none; a private message none.
func TestMapMemberRole(t *testing.T) {
	m := testMapper(t)
	for role, want := range map[string]otogi.ActorRole{"owner": otogi.RoleOwner, "admin": otogi.RoleAdmin, "member": "", "": "", "moderator": ""} {
		e := mapOne(t, m, "GROUP_MESSAGE_CREATE", `{"id":"r`+role+`","content":"hi","group_openid":"G1","author":{"member_openid":"M1","member_role":"`+role+`"}}`)
		if e.Actor.Role != want {
			t.Errorf("member_role %q: role %q, want %q", role, e.Actor.Role, want)
		}
	}
	if e := mapOne(t, m, "C2C_MESSAGE_CREATE", `{"id":"c9","content":"hi","author":{"user_openid":"U1","member_role":"owner"}}`); e.Actor.Role != "" {
		t.Errorf("a private message has role %q", e.Actor.Role)
	}
}
