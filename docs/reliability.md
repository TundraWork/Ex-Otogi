# Reliability

## Goal

The bot loses no inbound events, recovers on its own from ordinary failures (network drops, restarts, crashes), and makes every failure visible in the event log. Each mechanism here exists because of a failure that actually happens to a long-running IM bot.

## Failures and responses

| Failure | Response |
| --- | --- |
| Network or platform outage | The driver reconnects with backoff (gotd handles MTProto reconnects), then catches up on missed updates. |
| Platform rate limit (Telegram `FLOOD_WAIT`) | Wait the indicated time within the operation's context, then retry once the wait ends. |
| Language model rate limit or outage (HTTP 408, 429, 5xx, or no answer) | The provider retries up to 3 attempts in all, waiting what the server asks or backing off from 1s, but only before any output has been shown. A daily quota is not retried. Each attempt is in the call's record. |
| Process crash or kill | Unfinished durable inbox rows are dispatched again on restart. Sends are not repeated: they are recorded by idempotency key, and Telegram rejects a reused `random_id`. |
| Missed history cannot be recovered | Record a gap (kind, channel, time) and continue. For Telegram, gotd reports these through its gap hooks. |
| Module error or panic | Record the failed outcome or panic with its stack; other modules and events continue. Streams it left open are closed with their last content. |
| Platform rate limit during a streamed message | Skip updates until the wait ends (a later update supersedes them); `Close` waits it out, so the final content always arrives. |
| Required component fails | Record the cause, shut down gracefully, and exit non-zero so the supervisor restarts the process. |
| Invalid configuration or unreadable database at startup | Exit non-zero with a message naming the field or file. Never silently create fresh state over an unreadable database. |
| Event log write fails | Report it on stdout and in status; keep processing events. |

## Retries

Retry only operations that are safe to repeat (reads, or writes carrying an idempotency key). Retry in exactly one layer, normally the driver for platform calls, with a bounded number of attempts, exponential backoff, and context cancellation. Each retry and its final outcome are recorded. Validation and authorization errors are not retried.

## Durability

- The Telegram durable inbox, update state, and send records are committed to SQLite before being acknowledged.
- The kernel queue is in memory. It is safe because the inbox persists anything the kernel has not finished.
- The event log is durable once written and kept within its configured retention.

## Startup recovery

On start, a driver dispatches unfinished inbox rows, and the platform library catches up on history missed while offline (for Telegram, gotd's `getDifference`). The driver becomes ready once updates flow; a recorded gap does not block readiness.

If the Telegram inbox cannot be written, the driver stops gotd from saving its position and fails, so the process restarts and gotd re-fetches from the last saved position.
