// Package safety is the one place that says how cards show the pictures
// they must not show plainly: how far sensitive pictures are blurred, and
// whether a card the platform refused for its pictures goes out again under
// a sticker, which sticker, how see-through, and how large. It handles no
// events; it offers its settings to the card modules as the service
// cardimage.ServicePictures, and they restart with it when they change.
package safety

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/TundraWork/Ex-Otogi/pkg/cardimage"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Config is the module's settings; each unset field keeps the default.
type Config struct {
	Blur           *int     `json:"blur,omitempty" minimum:"0" maximum:"40" default:"9" doc:"How strongly pictures their source marks sensitive (an X post's sensitive media, an R-18 BOOTH item) are blurred, in pixels: enough to hide their details, not what they show. 0 shows them unblurred, still labeled."`
	CoverRefused   *bool    `json:"cover_refused,omitempty" default:"true" doc:"When a platform refuses a card for what its pictures show (QQ's 消息内容违规), send it once more with the sticker over each picture."`
	Sticker        string   `json:"sticker,omitempty" doc:"An image file on the server (PNG, WebP, GIF, or JPEG) to use as the sticker; empty uses the built-in one."`
	StickerOpacity *float64 `json:"sticker_opacity,omitempty" minimum:"0.1" maximum:"1" default:"0.65" doc:"How opaque the sticker is, from 0.1 to 1: lower lets more of the picture show through."`
	StickerSize    int      `json:"sticker_size,omitempty" minimum:"10" maximum:"100" default:"66" unit:"%" doc:"How much of a picture the sticker spans, in percent of its width and height."`
	CensoredVideos bool     `json:"censored_videos,omitempty" default:"false" doc:"Send the videos of a post whose pictures a card censors: one its source marks sensitive, or whose card the platform refused. Off keeps them back, as a raw video would undo the censoring."`
}

// Module is the content safety module.
type Module struct {
	pictures cardimage.Pictures
}

// New validates cfg, reads the sticker file when one is set, and returns
// the module.
func New(cfg Config) (*Module, error) {
	p := cardimage.DefaultPictures()
	var errs []error
	if cfg.Blur != nil {
		if *cfg.Blur < 0 || *cfg.Blur > 40 {
			errs = append(errs, &otogi.SettingError{Path: "blur", Err: fmt.Errorf("%d: want 0 to 40", *cfg.Blur)})
		}
		p.Blur = *cfg.Blur
	}
	if cfg.CoverRefused != nil {
		p.CoverRefused = *cfg.CoverRefused
	}
	p.CensoredVideos = cfg.CensoredVideos
	if cfg.StickerOpacity != nil {
		if *cfg.StickerOpacity < 0.1 || *cfg.StickerOpacity > 1 {
			errs = append(errs, &otogi.SettingError{Path: "sticker_opacity", Err: fmt.Errorf("%v: want 0.1 to 1", *cfg.StickerOpacity)})
		}
		p.StickerOpacity = *cfg.StickerOpacity
	}
	if cfg.StickerSize != 0 {
		if cfg.StickerSize < 10 || cfg.StickerSize > 100 {
			errs = append(errs, &otogi.SettingError{Path: "sticker_size", Err: fmt.Errorf("%d: want 10 to 100", cfg.StickerSize)})
		}
		p.StickerSize = cfg.StickerSize
	}
	if cfg.Sticker != "" {
		data, err := os.ReadFile(cfg.Sticker)
		switch mime := http.DetectContentType(data); {
		case err != nil:
			errs = append(errs, &otogi.SettingError{Path: "sticker", Err: err})
		case !strings.HasPrefix(mime, "image/"):
			errs = append(errs, &otogi.SettingError{Path: "sticker", Err: fmt.Errorf("%s: not an image (%s)", cfg.Sticker, mime)})
		default:
			p.Sticker = cardimage.DataURI(data, mime)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return &Module{pictures: p}, nil
}

// Descriptor declares the module: it handles no events and offers the
// cards' censoring.
func (*Module) Descriptor() otogi.ModuleDescriptor {
	return otogi.ModuleDescriptor{ID: "safety", Provides: []string{cardimage.ServicePictures}}
}

// Start offers the settings to the card modules.
func (m *Module) Start(_ context.Context, host otogi.Host) error {
	return host.Provide(cardimage.ServicePictures, m.pictures)
}

// Handle is never called: the module subscribes to nothing.
func (*Module) Handle(context.Context, otogi.Event, otogi.Invocation) error { return nil }
