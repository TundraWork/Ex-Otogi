package main

import (
	"context"
	"fmt"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

type destination struct {
	platform otogi.PlatformID
	account  otogi.AccountID
}

// driverHandle is what the router needs from a driver.
type driverHandle interface {
	otogi.Effector
	otogi.MediaFetcher
}

// router sends effects and media fetches to the driver of their (platform, account).
type router map[destination]driverHandle

func (r router) Apply(ctx context.Context, e otogi.Effect) (otogi.EventRef, error) {
	to := destination{e.Ref.Platform, e.Ref.Account}
	if e.Kind == otogi.EffectSend {
		to = destination{e.To.Platform, e.To.Account}
	}
	d, err := r.driver(to)
	if err != nil {
		return otogi.EventRef{}, err
	}
	return d.Apply(ctx, e)
}

func (r router) FetchMedia(ctx context.Context, from otogi.Destination, src otogi.MediaSource) ([]byte, error) {
	d, err := r.driver(destination{from.Platform, from.Account})
	if err != nil {
		return nil, err
	}
	return d.FetchMedia(ctx, from, src)
}

func (r router) driver(to destination) (driverHandle, error) {
	d, ok := r[to]
	if !ok {
		return nil, fmt.Errorf("no driver for platform %q account %q: %w", to.platform, to.account, otogi.ErrUnavailable)
	}
	return d, nil
}
