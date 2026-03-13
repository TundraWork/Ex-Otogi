package telegram

import (
	"container/list"
	"fmt"
	"strconv"
	"sync"

	"ex-otogi/pkg/otogi/platform"

	"github.com/gotd/td/tg"
)

const defaultMediaLocatorCacheEntries = 2048

type mediaLocatorCache struct {
	mu           sync.Mutex
	maxEntries   int
	byAttachment map[string]mediaLocatorRecord
	byMessage    map[string][]string
	lru          *list.List
	lruByKey     map[string]*list.Element
}

type mediaLocatorRecord struct {
	attachment MediaPayload
	location   tg.InputFileLocationClass
	inputPeer  tg.InputPeerClass
	messageKey string
}

type messageMediaLocator struct {
	attachment MediaPayload
	location   tg.InputFileLocationClass
}

type sizedPhoto interface {
	GetW() int
	GetH() int
	GetType() string
}

var (
	_ sizedPhoto = (*tg.PhotoSize)(nil)
	_ sizedPhoto = (*tg.PhotoCachedSize)(nil)
	_ sizedPhoto = (*tg.PhotoSizeProgressive)(nil)
)

func newMediaLocatorCache(maxEntries ...int) *mediaLocatorCache {
	limit := defaultMediaLocatorCacheEntries
	if len(maxEntries) > 0 && maxEntries[0] > 0 {
		limit = maxEntries[0]
	}

	return &mediaLocatorCache{
		maxEntries:   limit,
		byAttachment: make(map[string]mediaLocatorRecord),
		byMessage:    make(map[string][]string),
		lru:          list.New(),
		lruByKey:     make(map[string]*list.Element),
	}
}

func (c *mediaLocatorCache) RememberMessage(
	chat ChatRef,
	articleID string,
	peer tg.InputPeerClass,
	locators []messageMediaLocator,
) {
	if c == nil || chat.ID == "" || articleID == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	messageKey := mediaLocatorMessageKey(chat, articleID)
	c.forgetMessageLocked(messageKey)

	if peer == nil || len(locators) == 0 {
		return
	}

	for _, locator := range locators {
		if locator.attachment.ID == "" || locator.location == nil {
			continue
		}

		attachmentKey := mediaLocatorKey(chat, articleID, locator.attachment.ID)
		c.byAttachment[attachmentKey] = mediaLocatorRecord{
			attachment: locator.attachment,
			location:   cloneInputFileLocation(locator.location),
			inputPeer:  cloneInputPeer(peer),
			messageKey: messageKey,
		}
		c.byMessage[messageKey] = append(c.byMessage[messageKey], attachmentKey)
		c.touchLocked(attachmentKey)
		c.evictOverflowLocked()
	}
}

func (c *mediaLocatorCache) ForgetMessage(chat ChatRef, articleID string) {
	if c == nil || chat.ID == "" || articleID == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.forgetMessageLocked(mediaLocatorMessageKey(chat, articleID))
}

func (c *mediaLocatorCache) Lookup(request platform.MediaDownloadRequest) (mediaLocatorRecord, bool) {
	if c == nil {
		return mediaLocatorRecord{}, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	record, ok := c.byAttachment[mediaLocatorKey(ChatRef{
		ID:   request.Conversation.ID,
		Type: request.Conversation.Type,
	}, request.ArticleID, request.AttachmentID)]
	if !ok {
		return mediaLocatorRecord{}, false
	}
	c.touchLocked(mediaLocatorKey(ChatRef{
		ID:   request.Conversation.ID,
		Type: request.Conversation.Type,
	}, request.ArticleID, request.AttachmentID))

	return mediaLocatorRecord{
		attachment: record.attachment,
		location:   cloneInputFileLocation(record.location),
		inputPeer:  cloneInputPeer(record.inputPeer),
		messageKey: record.messageKey,
	}, true
}

func mediaLocatorKey(chat ChatRef, articleID string, attachmentID string) string {
	return conversationKey(chat.Type, chat.ID) + ":" + articleID + ":" + attachmentID
}

func mediaLocatorMessageKey(chat ChatRef, articleID string) string {
	return conversationKey(chat.Type, chat.ID) + ":" + articleID
}

func (c *mediaLocatorCache) touchLocked(key string) {
	if element, ok := c.lruByKey[key]; ok {
		c.lru.MoveToFront(element)
		return
	}

	c.lruByKey[key] = c.lru.PushFront(key)
}

func (c *mediaLocatorCache) evictOverflowLocked() {
	if c.maxEntries <= 0 {
		return
	}

	for len(c.byAttachment) > c.maxEntries {
		element := c.lru.Back()
		if element == nil {
			return
		}

		key, ok := element.Value.(string)
		if !ok {
			c.lru.Remove(element)
			continue
		}
		c.deleteAttachmentLocked(key)
	}
}

func (c *mediaLocatorCache) forgetMessageLocked(messageKey string) {
	keys := append([]string(nil), c.byMessage[messageKey]...)
	for _, key := range keys {
		c.deleteAttachmentLocked(key)
	}
	delete(c.byMessage, messageKey)
}

func (c *mediaLocatorCache) deleteAttachmentLocked(key string) {
	record, ok := c.byAttachment[key]
	if !ok {
		if element, exists := c.lruByKey[key]; exists {
			c.lru.Remove(element)
			delete(c.lruByKey, key)
		}
		return
	}

	delete(c.byAttachment, key)
	if element, exists := c.lruByKey[key]; exists {
		c.lru.Remove(element)
		delete(c.lruByKey, key)
	}

	if record.messageKey == "" {
		return
	}

	messageKeys := c.byMessage[record.messageKey]
	filtered := messageKeys[:0]
	for _, candidate := range messageKeys {
		if candidate != key {
			filtered = append(filtered, candidate)
		}
	}
	if len(filtered) == 0 {
		delete(c.byMessage, record.messageKey)
		return
	}
	c.byMessage[record.messageKey] = filtered
}

func cloneInputFileLocation(location tg.InputFileLocationClass) tg.InputFileLocationClass {
	switch typed := location.(type) {
	case *tg.InputDocumentFileLocation:
		copyLocation := *typed
		if len(typed.FileReference) > 0 {
			copyLocation.FileReference = append([]byte(nil), typed.FileReference...)
		}
		return &copyLocation
	case *tg.InputPhotoFileLocation:
		copyLocation := *typed
		if len(typed.FileReference) > 0 {
			copyLocation.FileReference = append([]byte(nil), typed.FileReference...)
		}
		return &copyLocation
	default:
		return location
	}
}

func buildMessageMediaLocators(media tg.MessageMediaClass) []messageMediaLocator {
	switch typed := media.(type) {
	case *tg.MessageMediaPhoto:
		photo, ok := typed.GetPhoto()
		if !ok || photo == nil {
			return nil
		}
		typedPhoto, ok := photo.(*tg.Photo)
		if !ok {
			return nil
		}
		photoID := mapPhotoID(typedPhoto)
		if photoID == "" {
			return nil
		}
		thumbSize := largestPhotoThumbSize(typedPhoto)
		if thumbSize == "" {
			return nil
		}

		return []messageMediaLocator{
			{
				attachment: MediaPayload{
					ID:   photoID,
					Type: platform.MediaTypePhoto,
				},
				location: &tg.InputPhotoFileLocation{
					ID:            typedPhoto.ID,
					AccessHash:    typedPhoto.AccessHash,
					FileReference: append([]byte(nil), typedPhoto.FileReference...),
					ThumbSize:     thumbSize,
				},
			},
		}
	case *tg.MessageMediaDocument:
		document, ok := typed.GetDocument()
		if !ok || document == nil {
			return nil
		}
		typedDocument, ok := document.(*tg.Document)
		if !ok {
			return nil
		}
		mapped := mapDocumentMedia(typedDocument)
		if len(mapped) == 0 {
			return nil
		}

		return []messageMediaLocator{
			{
				attachment: mapped[0],
				location:   typedDocument.AsInputDocumentFileLocation(),
			},
		}
	default:
		return nil
	}
}

func largestPhotoThumbSize(photo *tg.Photo) string {
	if photo == nil {
		return ""
	}

	var (
		thumbSize  string
		maxW, maxH int
	)
	for _, item := range photo.Sizes {
		size, ok := item.(sizedPhoto)
		if !ok {
			continue
		}
		if maxW < size.GetW() && maxH < size.GetH() {
			thumbSize = size.GetType()
			maxW = size.GetW()
			maxH = size.GetH()
		}
	}

	return thumbSize
}

func (m DefaultGotdUpdateMapper) rememberMessageMediaLocators(
	chat ChatRef,
	message *tg.Message,
	envelope gotdUpdateEnvelope,
) {
	if m.mediaLocatorCache == nil || message == nil || chat.ID == "" {
		return
	}

	locators := buildMessageMediaLocators(message.Media)
	if len(locators) == 0 {
		m.mediaLocatorCache.ForgetMessage(chat, strconv.Itoa(message.ID))
		return
	}

	peer := resolveInputPeerFromPeer(message.PeerID, envelope)
	if peer == nil {
		m.mediaLocatorCache.ForgetMessage(chat, strconv.Itoa(message.ID))
		return
	}

	m.mediaLocatorCache.RememberMessage(chat, strconv.Itoa(message.ID), peer, locators)
}

func mediaLocatorRecordAttachment(record mediaLocatorRecord) (platform.MediaAttachment, error) {
	mapped := mapMedia([]MediaPayload{record.attachment})
	if len(mapped) != 1 {
		return platform.MediaAttachment{}, fmt.Errorf("%w: invalid mapped attachment", platform.ErrMediaDownloadNotFound)
	}

	return mapped[0], nil
}
