// Package platform defines the platform-facing surface of the Otogi Protocol.
//
// The platform package is where drivers and modules meet on shared content and
// operation contracts: inbound events, articles, media, markdown entities,
// commands, outbound delivery, moderation, and framework-level article tags.
// Driver implementations normalize platform-native payloads into these types,
// and modules consume and produce content only through these contracts.
package platform
