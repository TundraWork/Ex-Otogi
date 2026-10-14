# Product

## Mission

Ex-Otogi is a self-hosted bot runtime. It receives events from IM platforms, routes them to compiled-in modules, and sends their responses back through platform drivers.

It optimizes, in order, for easy operation, easy maintenance, a well-designed core, and flexibility to add platforms and modules. Feature count is secondary.

## Actors

- **Participant:** talks to the bot through an IM platform.
- **Operator:** configures, runs, inspects, backs up, and upgrades one installation. Usually also the developer.
- **Module author:** writes bot behavior against the module SDK.
- **Driver author:** adapts an IM platform to the rich event model.

## Capabilities

The runtime:

1. receives platform events and normalizes them into protocol-neutral rich events;
2. routes events to subscribed modules with bounded concurrency;
3. gives modules capabilities for messaging and other side effects;
4. persists inbound platform events durably so that nothing is lost across restarts or crashes;
5. records every event, module outcome, outbound action, and failure in a structured event log;
6. lets operators query that log and check runtime status through a management plane;
7. starts, drains, and stops cleanly.

## Platforms

Telegram is the first platform, running as a **user account** over MTProto (gotd). The event model is designed so other protocols (Discord, Matrix, QQ, and others) can be added as drivers without kernel changes.

## Non-goals

- distributed or multi-node execution;
- dynamically loaded or out-of-process plugins (modules are compiled in);
- a multi-tenant hosted service;
- uninterrupted service during host failure;
- compatibility with the `master` implementation;
- long-term memory and control-panel UX, until each has its own design.
