package discord

import (
	"context"
	"fmt"

	"github.com/bwmarrin/discordgo"
)

const defaultDiscordgoUpdateBuffer = 1024

// DiscordgoSessionClient abstracts the discordgo.Session lifecycle used by DiscordgoSource.
type DiscordgoSessionClient interface {
	// Open establishes the Discord Gateway websocket connection.
	Open() error
	// Close terminates the Discord Gateway websocket connection.
	Close() error
	// AddHandler registers a typed event handler and returns a removal function.
	AddHandler(handler interface{}) func()
}

// DiscordgoEventMapper maps raw discordgo gateway events to adapter Update DTOs.
type DiscordgoEventMapper interface {
	// Map converts a raw discordgo event to an adapter Update DTO.
	// The accepted flag allows skipping unsupported event types.
	Map(ctx context.Context, raw any) (Update, bool, error)
}

// DiscordgoSource wires discordgo gateway events into the UpdateSource contract.
type DiscordgoSource struct {
	client DiscordgoSessionClient
	mapper DiscordgoEventMapper
	buffer int
}

// NewDiscordgoSource creates a source backed by a discordgo session.
func NewDiscordgoSource(client DiscordgoSessionClient, mapper DiscordgoEventMapper, buffer int) (*DiscordgoSource, error) {
	if client == nil {
		return nil, fmt.Errorf("new discordgo source: nil client")
	}
	if mapper == nil {
		return nil, fmt.Errorf("new discordgo source: nil mapper")
	}
	if buffer <= 0 {
		buffer = defaultDiscordgoUpdateBuffer
	}

	return &DiscordgoSource{
		client: client,
		mapper: mapper,
		buffer: buffer,
	}, nil
}

// Consume opens the Discord Gateway, forwards incoming events to handler, and exits on context cancellation.
func (s *DiscordgoSource) Consume(ctx context.Context, handler UpdateHandler) error {
	if handler == nil {
		return fmt.Errorf("discordgo source consume: nil handler")
	}

	updateCh := make(chan any, s.buffer)
	s.registerHandlers(updateCh)

	if err := s.client.Open(); err != nil {
		return fmt.Errorf("discordgo source open: %w", err)
	}
	defer func() { _ = s.client.Close() }()

	for {
		select {
		case <-ctx.Done():
			return nil
		case raw, ok := <-updateCh:
			if !ok {
				return nil
			}
			updates, err := s.mapSafely(ctx, raw)
			if err != nil {
				return fmt.Errorf("discordgo source map event: %w", err)
			}
			for _, update := range updates {
				if err := handler(ctx, update); err != nil {
					return fmt.Errorf("discordgo source handle update %s: %w", update.Type, err)
				}
			}
		}
	}
}

// registerHandlers sets up all discordgo event handlers to forward raw events to updateCh.
func (s *DiscordgoSource) registerHandlers(updateCh chan<- any) {
	s.client.AddHandler(func(_ *discordgo.Session, e *discordgo.MessageCreate) {
		s.publish(updateCh, e)
	})
	s.client.AddHandler(func(_ *discordgo.Session, e *discordgo.MessageUpdate) {
		s.publish(updateCh, e)
	})
	s.client.AddHandler(func(_ *discordgo.Session, e *discordgo.MessageDelete) {
		s.publish(updateCh, e)
	})
	s.client.AddHandler(func(_ *discordgo.Session, e *discordgo.MessageReactionAdd) {
		s.publish(updateCh, e)
	})
	s.client.AddHandler(func(_ *discordgo.Session, e *discordgo.MessageReactionRemove) {
		s.publish(updateCh, e)
	})
	s.client.AddHandler(func(_ *discordgo.Session, e *discordgo.GuildMemberAdd) {
		s.publish(updateCh, e)
	})
	s.client.AddHandler(func(_ *discordgo.Session, e *discordgo.GuildMemberRemove) {
		s.publish(updateCh, e)
	})
}

// publish sends a raw event to the update channel, dropping silently if the channel is full.
func (s *DiscordgoSource) publish(updateCh chan<- any, event any) {
	select {
	case updateCh <- event:
	default:
	}
}

// mapSafely protects mapper panics at the adapter boundary.
func (s *DiscordgoSource) mapSafely(ctx context.Context, raw any) (updates []Update, err error) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		err = fmt.Errorf("map discordgo event panic: %v", recovered)
	}()

	update, accepted, mapErr := s.mapper.Map(ctx, raw)
	if mapErr != nil {
		return nil, fmt.Errorf("map discordgo event: %w", mapErr)
	}
	if !accepted {
		return nil, nil
	}

	return []Update{update}, nil
}
