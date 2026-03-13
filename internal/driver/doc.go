// Package driver standardizes how platform runtimes are constructed for Otogi.
//
// Builders in this package adapt platform SDKs into the Otogi driver, sink,
// media, moderation, and related contracts. Registry.BuildEnabled applies
// shared runtime policies expected from Otogi drivers, such as stable source
// identity handling and best-effort article tag bridging between outbound
// sends and later self-authored inbound article events.
package driver
