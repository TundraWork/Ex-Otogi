// Package media is the contract of the media service: descriptions of the
// images, animations, and videos people send, kept by content. The media
// module provides it as Service, describing media itself as messages
// arrive; a module that reads descriptions lists Service in its
// descriptor's Requires and looks it up with otogi.Lookup[Describer]. The
// module also provides StickersService, the pictures operators keep for
// agents to send, looked up with otogi.Lookup[Stickers].
package media

import (
	"context"
	"errors"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Service is the name the media module provides Describer under.
const Service = "media"

// Kind says what a piece of media is.
type Kind string

// The kinds of media a description gives.
const (
	KindScreenshot Kind = "screenshot"
	KindPhoto      Kind = "photo"
	KindSticker    Kind = "sticker"
	KindMeme       Kind = "meme"
	KindVideo      Kind = "video"
	KindOther      Kind = "other"
)

// Description says what a piece of media shows.
type Description struct {
	// Kind is what the media is.
	Kind Kind
	// Summary is what it shows and, for a sticker or meme, what it conveys,
	// in the configured language.
	Summary string
	// Text is the text on it, or what a video says and its captions,
	// verbatim; empty when there is none.
	Text string
}

var (
	// ErrNotAllowed reports media of a chat that is not on the media
	// module's describe list.
	ErrNotAllowed = errors.New("media: chat not on the describe list")
	// ErrNotDescribed reports media of a kind or size that is not described.
	ErrNotDescribed = errors.New("media: not described")
)

// Describer gives the descriptions the media module keeps. The module
// describes media itself, as messages arrive; callers only wait for or
// look up the result. It is safe for concurrent use.
type Describer interface {
	// Describe returns the description of m, seen in from, waiting until
	// ctx ends for the module to finish with it. It fails with
	// ErrNotAllowed or ErrNotDescribed without waiting, with
	// otogi.ErrUnavailable when every configured model failed, and with
	// ctx's error when the wait ran out.
	Describe(ctx context.Context, from otogi.Destination, m otogi.Media) (Description, error)
	// Lookup returns the kept description of m, seen in from, without
	// waiting; ok is false when there is none.
	Lookup(ctx context.Context, from otogi.Destination, m otogi.Media) (d Description, ok bool, err error)
}

// StickersService is the name the media module provides Stickers under.
const StickersService = "media.stickers"

// Sticker is a picture operators keep for agents to send.
type Sticker struct {
	// ID is the sticker's number; it is never reused.
	ID int
	// Kind is what its description said it is; empty when it was not
	// described.
	Kind Kind
	// Text is what a list of stickers shows: the operator's note, else the
	// description's summary.
	Text string
}

// ErrNoSticker reports a sticker number that is not kept.
var ErrNoSticker = errors.New("media: no such sticker")

// Stickers gives the stickers operators keep with the media module's
// /sticker command. It is safe for concurrent use.
type Stickers interface {
	// List returns the stickers by number, leaving out those with neither
	// a note nor a description.
	List(ctx context.Context) ([]Sticker, error)
	// Picture returns sticker id's picture as inline media ready to send:
	// MediaAnimation for a GIF, MediaImage otherwise, with its MediaType and
	// its SHA-256 in Hashes. It fails with ErrNoSticker when there is no
	// such sticker.
	Picture(ctx context.Context, id int) (otogi.Media, Sticker, error)
}
