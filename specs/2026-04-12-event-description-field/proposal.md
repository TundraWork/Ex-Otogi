# Proposal: Add Description Field to Management Events

## Original Requirement

给日志事件增加一个"描述"字段，用于写事件的简要描述。对类似 article.created 这样的事件，把 Article.Text 写进去。也不用全长，就把前 140 字存下来就行了。再比如 llm.call.completed 也是一样。类似的事件，有 payload 的，都看一遍，能写额外描述的，都写进去。

## Summary

Add a `Description` field to management events (`panel.Event`) that carries a brief human-readable excerpt derived from the event's payload data. The field should store up to 140 Unicode characters of relevant content (e.g., article text for `platform.event.received`, response text for `llm.call.completed`). All event emission sites should be reviewed and enriched where payload data can produce a meaningful description.
