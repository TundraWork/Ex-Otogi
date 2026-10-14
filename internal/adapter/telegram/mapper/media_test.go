package mapper

import (
	"testing"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
	"github.com/gotd/td/tg"
)

func TestKnownMediaGetsTypedProjectionAndRawExtension(t *testing.T) {
	cases := []struct {
		media tg.MessageMediaClass
		kind  otogi.PartKind
	}{
		{&tg.MessageMediaGeo{Geo: &tg.GeoPoint{Lat: 12.5, Long: -34.25}}, otogi.PartLocation},
		{&tg.MessageMediaContact{FirstName: "Ada", LastName: "Lovelace", PhoneNumber: "+1"}, otogi.PartContact},
	}
	for _, tc := range cases {
		message := &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 2}, Date: 100, Media: tc.media}
		message.SetMedia(tc.media)
		mapped, err := New(testConfig(t)).Map(&tg.UpdateNewMessage{Message: message, Pts: 1, PtsCount: 1})
		if err != nil {
			t.Fatal(err)
		}
		part := mapped.Event.Message.Parts[0]
		if part.Kind != tc.kind || len(part.Extensions) != 1 || part.Extensions[0].Name != "message-media" {
			t.Fatalf("part kind=%s extensions=%v", part.Kind, part.Extensions)
		}
	}
}

func TestDocumentAndPhotoUseOpaqueRefreshLocators(t *testing.T) {
	document := &tg.Document{ID: 10, AccessHash: 11, FileReference: []byte{12}, MimeType: "application/pdf", Size: 99, DCID: 4}
	docMedia := &tg.MessageMediaDocument{}
	docMedia.SetDocument(document)
	photo := &tg.Photo{ID: 20, AccessHash: 21, FileReference: []byte{22}, DCID: 5}
	photoMedia := &tg.MessageMediaPhoto{}
	photoMedia.SetPhoto(photo)
	for _, tc := range []struct {
		media tg.MessageMediaClass
		role  otogi.MediaRole
	}{{docMedia, otogi.MediaDocument}, {photoMedia, otogi.MediaImage}} {
		message := &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 2}, Date: 100}
		message.SetMedia(tc.media)
		mapped, err := New(testConfig(t)).Map(&tg.UpdateNewMessage{Message: message, Pts: 1, PtsCount: 1})
		if err != nil {
			t.Fatal(err)
		}
		media := mapped.Event.Message.Parts[0].Media
		if media == nil || media.Role != tc.role || media.Source.Kind != otogi.MediaSourceOpaque {
			t.Fatalf("media role=%s source=%s", media.Role, media.Source.Kind)
		}
		locator := media.Source.Locator
		decoded, err := DecodeMediaLocator(locator.Data)
		if err != nil || decoded.ID == 0 || len(decoded.FileReference) == 0 {
			t.Fatalf("locator=%+v err=%v", decoded, err)
		}
	}
}
