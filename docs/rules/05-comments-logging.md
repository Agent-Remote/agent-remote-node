# 05 Comments And Logging

Every exported type, function, method, constant, and variable must have a concise Go-style comment that begins with the declaration name. Inline comments explain security decisions, compatibility workarounds, or non-obvious failure handling; they must not narrate the code.

Logs may contain operation names, task IDs, node IDs, statuses, retry counts, durations, and bounded public errors. Logs must never contain node tokens, registration tokens, private keys, cookies, browser contents, tool login state, raw configuration, or unrestricted task payloads.

Keep error messages actionable but bounded. When wrapping external command failures, avoid echoing secret-bearing arguments or environment values.
