// Package discord implements Discord adapter components that translate
// Discord Gateway events and REST API operations into the Otogi Protocol.
//
// The adapter bridges the Discord bot API (via discordgo) into normalized
// Otogi events for inbound traffic and translates outbound Otogi operations
// back into Discord REST API calls. Platform SDK types are kept internal to
// this package and do not leak across the driver boundary.
package discord
