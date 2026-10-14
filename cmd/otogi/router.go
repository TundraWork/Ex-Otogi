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

// router delivers outbound messages to the driver of their (platform, account).
type router map[destination]otogi.Messenger

func (r router) Send(ctx context.Context, msg otogi.OutboundMessage) (otogi.EventRef, error) {
	m, ok := r[destination{msg.Platform, msg.Account}]
	if !ok {
		return otogi.EventRef{}, fmt.Errorf("no driver for platform %q account %q: %w", msg.Platform, msg.Account, otogi.ErrUnavailable)
	}
	return m.Send(ctx, msg)
}
