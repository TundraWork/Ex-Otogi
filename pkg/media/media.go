// Package media is the contract of the media service: descriptions of the
// images, animations, and videos people send, kept by content. The media
// module provides it as Service, describing media itself as messages
// arrive; a module that reads descriptions lists Service in its
// descriptor's Requires and looks it up with otogi.Lookup[Describer]. The
// module also provides StickersService, the pictures operators keep for
// agents to send, looked up with otogi.Lookup[Stickers], and
// StickerAdminService, through which the management plane manages them.
package media

import (
	"context"
	"errors"
	"time"

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
	// Sticker is the number of the kept sticker the media is, 0 when it is
	// not one. Summary is then the sticker's text: the operator's note, else
	// the summary it was kept with.
	Sticker int
	// ID names the media to an agent, for Examine: the first six hex digits
	// of the digest it is described under. Empty for a kept sticker, which
	// its number names.
	ID string
}

var (
	// ErrNotAllowed reports media of a chat that is not on the media
	// module's describe list.
	ErrNotAllowed = errors.New("media: chat not on the describe list")
	// ErrNotDescribed reports media of a kind or size that is not described.
	ErrNotDescribed = errors.New("media: not described")
	// ErrNotKept reports media given to Examine without its bytes whose
	// original the module does not keep for that chat.
	ErrNotKept = errors.New("media: original not kept")
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
	// Examine answers question about m, seen in from, asking the configured
	// models in turn. m carries its bytes inline, or else its original is
	// the one the module keeps from that chat. It fails with ErrNotKept when
	// m has no bytes and none is kept, with ErrNotAllowed or ErrNotDescribed
	// as Describe does, and with otogi.ErrUnavailable when every model
	// failed.
	Examine(ctx context.Context, from otogi.Destination, m otogi.Media, question string) (string, error)
	// Original returns m as the module keeps it from from: its bytes
	// inline, and its type as told by them. It fails with ErrNotAllowed
	// for a chat not on the describe list, and with ErrNotKept when the
	// module keeps no original of m seen in that chat.
	Original(ctx context.Context, from otogi.Destination, m otogi.Media) (otogi.Media, error)
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
	// Packs are the IDs of the packs it is in, sorted; none when it is in
	// none.
	Packs []string
}

// ErrNoSticker reports a sticker number that is not kept.
var ErrNoSticker = errors.New("media: no such sticker")

// Errors of sticker packs: ErrNoPack, a pack ID no pack has; ErrPackExists,
// one a pack has already; ErrInvalidPack, an ID not matching PackIDPattern
// or an empty title.
var (
	ErrNoPack      = errors.New("media: no such sticker pack")
	ErrPackExists  = errors.New("media: sticker pack exists")
	ErrInvalidPack = errors.New("media: invalid sticker pack")
)

// PackIDPattern is what a sticker pack's ID matches: lowercase letters,
// digits, and hyphens, starting with a letter or digit, at most 32.
const PackIDPattern = `^[a-z0-9][a-z0-9-]{0,31}$`

// StickerPack is a named list of kept stickers, to give agents together. A
// sticker may be in several packs, or in none. ID is how chat's settings
// name it, fixed once it is made; Title and Description may change.
type StickerPack struct {
	ID          string
	Title       string
	Description string
	// Stickers are its stickers' numbers, in order.
	Stickers []int
}

// RemovePack says what removing a pack does to its stickers.
type RemovePack string

const (
	// KeepStickers keeps them: those in no other pack are in none.
	KeepStickers RemovePack = "keep"
	// RemoveStickers removes those in no other pack too.
	RemoveStickers RemovePack = "remove"
)

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

// StickerAdminService is the name the media module provides StickerAdmin
// under. The management plane looks it up in the running modules.
const StickerAdminService = "media.stickers.admin"

// KeptSticker is a kept sticker as an operator manages it.
type KeptSticker struct {
	Sticker // ID, Kind, Text, and Packs, as Stickers gives them
	// Summary is what its description said when it was kept; Note, the
	// operator's note.
	Summary, Note string
	// MediaType is image/png, image/jpeg, or image/gif; Size, its bytes.
	MediaType string
	Size      int64
	SHA256    []byte
	// AddedBy is who kept it: platform:actor, or the management plane's
	// caller; AddedAt, when.
	AddedBy string
	AddedAt time.Time
}

// StickerRefusal is a picture not kept, and why: Reason is what the
// media.sticker record says (too large, not png, jpeg, or gif, already
// kept); Message is the reply /sticker add gives; ID is the sticker
// already kept, for already kept.
type StickerRefusal struct {
	Reason  string
	Message string
	ID      int
}

func (e *StickerRefusal) Error() string { return "media: sticker not kept: " + e.Reason }

// StickerAdmin manages the stickers kept with /sticker, as the command
// does, and their packs: the same checks, and a record for each change
// (media.sticker, media.sticker_pack), by the given actor and on no event.
// It is safe for concurrent use.
type StickerAdmin interface {
	// All returns every kept sticker by number, those with neither a note
	// nor a description included, and every pack by ID.
	All(ctx context.Context) ([]KeptSticker, []StickerPack, error)
	// Picture is Stickers.Picture.
	Picture(ctx context.Context, id int) (otogi.Media, Sticker, error)
	// Keep keeps data, with no note, describing it first as /sticker add
	// does, waiting for the description at most 30 seconds, and puts it in
	// pack unless pack is empty. A picture not kept fails with
	// *StickerRefusal. One kept already is refused when pack is empty, and
	// otherwise put in pack, existing then. A pack that is not there fails
	// with ErrNoPack, before the picture is described.
	Keep(ctx context.Context, data []byte, pack, by string) (st KeptSticker, existing bool, err error)
	// SetNote sets sticker id's note; an empty note clears it. It fails with
	// ErrNoSticker when there is no such sticker.
	SetNote(ctx context.Context, id int, note, by string) (KeptSticker, error)
	// Remove removes sticker id, from its packs too; its number is not used
	// again. It fails with ErrNoSticker when there is no such sticker.
	Remove(ctx context.Context, id int, by string) error

	// MakePack makes pack p, with no stickers. It fails with ErrInvalidPack
	// when p's ID does not match PackIDPattern or its title is empty, and
	// with ErrPackExists when a pack has its ID.
	MakePack(ctx context.Context, p StickerPack, by string) (StickerPack, error)
	// EditPack sets pack id's title and description. It fails with
	// ErrNoPack, or ErrInvalidPack for an empty title.
	EditPack(ctx context.Context, id, title, description, by string) (StickerPack, error)
	// RemovePack removes pack id and, with RemoveStickers, the stickers in
	// no other pack, returning those it removed. It fails with ErrNoPack.
	RemovePack(ctx context.Context, id string, stickers RemovePack, by string) (removed []int, err error)
	// Pack puts sticker into pack; Unpack takes it out. Either does nothing
	// when it is so already, and fails with ErrNoPack or ErrNoSticker.
	Pack(ctx context.Context, pack string, sticker int, by string) error
	Unpack(ctx context.Context, pack string, sticker int, by string) error
}
