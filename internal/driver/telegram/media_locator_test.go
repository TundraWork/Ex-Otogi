package telegram

import (
	"context"
	"testing"
	"time"

	"ex-otogi/pkg/otogi"

	"github.com/gotd/td/tg"
)

func TestDefaultGotdUpdateMapperCapturesPhotoMediaLocator(t *testing.T) {
	t.Parallel()

	cache := newMediaLocatorCache()
	mapper := NewDefaultGotdUpdateMapper(WithMediaLocatorCache(cache))
	media := &tg.MessageMediaPhoto{}
	media.SetPhoto(&tg.Photo{
		ID:            987,
		AccessHash:    654,
		FileReference: []byte{1, 2, 3},
		Date:          1_700_000_000,
		Sizes: []tg.PhotoSizeClass{
			&tg.PhotoSize{Type: "m", W: 320, H: 320},
			&tg.PhotoSize{Type: "x", W: 800, H: 800},
		},
	})

	_, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateNewMessage{
			Message: &tg.Message{
				ID:      777,
				PeerID:  &tg.PeerChat{ChatID: 100},
				Date:    1_700_000_000,
				Message: "photo",
				FromID:  &tg.PeerUser{UserID: 42},
				Media:   media,
			},
		},
		occurredAt: time.Unix(1_700_000_000, 0).UTC(),
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			100: {title: "group-chat", kind: otogi.ConversationTypeGroup},
		},
		updateClass: "updateNewMessage",
	})
	if err != nil {
		t.Fatalf("map failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted message")
	}

	record, ok := cache.Lookup(otogi.MediaDownloadRequest{
		Source: otogi.EventSource{
			Platform: otogi.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: otogi.Conversation{
			ID:   "100",
			Type: otogi.ConversationTypeGroup,
		},
		ArticleID:    "777",
		AttachmentID: "987",
	})
	if !ok {
		t.Fatal("expected cached photo locator")
	}
	if record.attachment.ID != "987" {
		t.Fatalf("attachment id = %q, want 987", record.attachment.ID)
	}
	location, ok := record.location.(*tg.InputPhotoFileLocation)
	if !ok {
		t.Fatalf("location type = %T, want *tg.InputPhotoFileLocation", record.location)
	}
	if location.ThumbSize != "x" {
		t.Fatalf("thumb size = %q, want x", location.ThumbSize)
	}
	if location.ID != 987 || location.AccessHash != 654 {
		t.Fatalf("location = %+v, want id=987 access_hash=654", location)
	}
}

func TestDefaultGotdUpdateMapperCapturesDocumentMediaLocator(t *testing.T) {
	t.Parallel()

	cache := newMediaLocatorCache()
	mapper := NewDefaultGotdUpdateMapper(WithMediaLocatorCache(cache))
	media := &tg.MessageMediaDocument{}
	media.SetDocument(&tg.Document{
		ID:            4321,
		AccessHash:    7654,
		FileReference: []byte{9, 8, 7},
		Date:          1_700_000_010,
		MimeType:      "text/plain",
		Size:          123,
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeFilename{FileName: "notes.txt"},
		},
	})

	_, accepted, err := mapper.Map(context.Background(), gotdUpdateEnvelope{
		update: &tg.UpdateNewChannelMessage{
			Message: &tg.Message{
				ID:      778,
				PeerID:  &tg.PeerChannel{ChannelID: 500},
				Date:    1_700_000_010,
				Message: "document",
				FromID:  &tg.PeerUser{UserID: 42},
				Media:   media,
			},
		},
		occurredAt: time.Unix(1_700_000_010, 0).UTC(),
		usersByID: map[int64]*tg.User{
			42: newTGUser(42, "alice", "Alice", "User", false),
		},
		chatsByID: map[int64]gotdChatInfo{
			500: {
				title:     "channel",
				kind:      otogi.ConversationTypeChannel,
				inputPeer: &tg.InputPeerChannel{ChannelID: 500, AccessHash: 111},
			},
		},
		updateClass: "updateNewChannelMessage",
	})
	if err != nil {
		t.Fatalf("map failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted message")
	}

	record, ok := cache.Lookup(otogi.MediaDownloadRequest{
		Source: otogi.EventSource{
			Platform: otogi.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: otogi.Conversation{
			ID:   "500",
			Type: otogi.ConversationTypeChannel,
		},
		ArticleID:    "778",
		AttachmentID: "4321",
	})
	if !ok {
		t.Fatal("expected cached document locator")
	}
	if record.attachment.FileName != "notes.txt" {
		t.Fatalf("file name = %q, want notes.txt", record.attachment.FileName)
	}
	location, ok := record.location.(*tg.InputDocumentFileLocation)
	if !ok {
		t.Fatalf("location type = %T, want *tg.InputDocumentFileLocation", record.location)
	}
	if location.ID != 4321 || location.AccessHash != 7654 {
		t.Fatalf("location = %+v, want id=4321 access_hash=7654", location)
	}
	peer, ok := record.inputPeer.(*tg.InputPeerChannel)
	if !ok {
		t.Fatalf("input peer type = %T, want *tg.InputPeerChannel", record.inputPeer)
	}
	if peer.ChannelID != 500 {
		t.Fatalf("channel id = %d, want 500", peer.ChannelID)
	}
}

func TestDefaultGotdUpdateMapperClearsMediaLocatorWhenMediaDisappears(t *testing.T) {
	t.Parallel()

	cache := newMediaLocatorCache()
	cache.RememberMessage(
		ChatRef{ID: "100", Type: otogi.ConversationTypeGroup},
		"777",
		&tg.InputPeerChat{ChatID: 100},
		[]messageMediaLocator{
			{
				attachment: MediaPayload{
					ID:   "987",
					Type: otogi.MediaTypePhoto,
				},
				location: &tg.InputPhotoFileLocation{
					ID:            987,
					AccessHash:    654,
					FileReference: []byte{1, 2, 3},
					ThumbSize:     "x",
				},
			},
		},
	)
	mapper := NewDefaultGotdUpdateMapper(WithMediaLocatorCache(cache))

	mapper.rememberMessageMediaLocators(
		ChatRef{ID: "100", Type: otogi.ConversationTypeGroup},
		&tg.Message{
			ID:     777,
			PeerID: &tg.PeerChat{ChatID: 100},
		},
		gotdUpdateEnvelope{},
	)

	if _, ok := cache.Lookup(otogi.MediaDownloadRequest{
		Conversation: otogi.Conversation{
			ID:   "100",
			Type: otogi.ConversationTypeGroup,
		},
		ArticleID:    "777",
		AttachmentID: "987",
	}); ok {
		t.Fatal("expected stale media locator to be cleared")
	}
}

func TestMediaLocatorCacheReplacesMessageEntries(t *testing.T) {
	t.Parallel()

	cache := newMediaLocatorCache()
	chat := ChatRef{ID: "100", Type: otogi.ConversationTypeGroup}
	peer := &tg.InputPeerChat{ChatID: 100}

	cache.RememberMessage(chat, "55", peer, []messageMediaLocator{
		{
			attachment: MediaPayload{ID: "old", Type: otogi.MediaTypeDocument},
			location:   &tg.InputDocumentFileLocation{ID: 1, AccessHash: 10},
		},
	})
	cache.RememberMessage(chat, "55", peer, []messageMediaLocator{
		{
			attachment: MediaPayload{ID: "new", Type: otogi.MediaTypeDocument},
			location:   &tg.InputDocumentFileLocation{ID: 2, AccessHash: 20},
		},
	})

	if _, ok := cache.Lookup(otogi.MediaDownloadRequest{
		Conversation: otogi.Conversation{ID: "100", Type: otogi.ConversationTypeGroup},
		ArticleID:    "55",
		AttachmentID: "old",
	}); ok {
		t.Fatal("expected replaced attachment to be evicted")
	}
	record, ok := cache.Lookup(otogi.MediaDownloadRequest{
		Conversation: otogi.Conversation{ID: "100", Type: otogi.ConversationTypeGroup},
		ArticleID:    "55",
		AttachmentID: "new",
	})
	if !ok {
		t.Fatal("expected new attachment to remain cached")
	}
	location, ok := record.location.(*tg.InputDocumentFileLocation)
	if !ok {
		t.Fatalf("location type = %T, want *tg.InputDocumentFileLocation", record.location)
	}
	if location.ID != 2 {
		t.Fatalf("location id = %d, want 2", location.ID)
	}
}

func TestMediaLocatorCacheEvictsLeastRecentlyUsed(t *testing.T) {
	t.Parallel()

	cache := newMediaLocatorCache(2)
	chat := ChatRef{ID: "100", Type: otogi.ConversationTypeGroup}
	peer := &tg.InputPeerChat{ChatID: 100}

	cache.RememberMessage(chat, "1", peer, []messageMediaLocator{
		{
			attachment: MediaPayload{ID: "a", Type: otogi.MediaTypeDocument},
			location:   &tg.InputDocumentFileLocation{ID: 1, AccessHash: 10},
		},
	})
	cache.RememberMessage(chat, "2", peer, []messageMediaLocator{
		{
			attachment: MediaPayload{ID: "b", Type: otogi.MediaTypeDocument},
			location:   &tg.InputDocumentFileLocation{ID: 2, AccessHash: 20},
		},
	})

	if _, ok := cache.Lookup(otogi.MediaDownloadRequest{
		Conversation: otogi.Conversation{ID: "100", Type: otogi.ConversationTypeGroup},
		ArticleID:    "1",
		AttachmentID: "a",
	}); !ok {
		t.Fatal("expected first attachment lookup to succeed")
	}

	cache.RememberMessage(chat, "3", peer, []messageMediaLocator{
		{
			attachment: MediaPayload{ID: "c", Type: otogi.MediaTypeDocument},
			location:   &tg.InputDocumentFileLocation{ID: 3, AccessHash: 30},
		},
	})

	if _, ok := cache.Lookup(otogi.MediaDownloadRequest{
		Conversation: otogi.Conversation{ID: "100", Type: otogi.ConversationTypeGroup},
		ArticleID:    "2",
		AttachmentID: "b",
	}); ok {
		t.Fatal("expected least-recently-used attachment to be evicted")
	}
	if _, ok := cache.Lookup(otogi.MediaDownloadRequest{
		Conversation: otogi.Conversation{ID: "100", Type: otogi.ConversationTypeGroup},
		ArticleID:    "1",
		AttachmentID: "a",
	}); !ok {
		t.Fatal("expected recently touched attachment to remain cached")
	}
	if _, ok := cache.Lookup(otogi.MediaDownloadRequest{
		Conversation: otogi.Conversation{ID: "100", Type: otogi.ConversationTypeGroup},
		ArticleID:    "3",
		AttachmentID: "c",
	}); !ok {
		t.Fatal("expected newest attachment to remain cached")
	}
}
