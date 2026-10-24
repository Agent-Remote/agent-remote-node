# Exact session attachment gateway contract

The current `agent-remote-attach` gateway supports the exact terminal and its
separate `session_attachment_transfer` v1 channel. The ordinary receiver and
its account path are not valid authorization for an exact session.

The fixed gateway command opens one transfer lease containing the
canonical session UUID, expected device UUID and a fresh lease UUID. The
gateway derives the actual SSH key identity from the forced-command connection
and sends `node_id`, `session_id`, `expected_device_id`, `ssh_key_id` and
`lease_id` to the Server's exact verification route. It must derive the runtime
account directory from the verified session; client frames never provide an
account path.

The gateway first sends a strict `lease_offer` with the complete identity,
expiry and limits. Every `open`, `upload` and `cleanup` frame then repeats session, expected device and
lease identity. The gateway rejects unknown JSON fields, changed identities,
path separators, traversal, links, special files, size/checksum violations and
expired or replayed batches. File and image bytes are staged beneath a
session-and-lease directory with the existing owner and runtime permission
rules. An upload acknowledgement includes only the derived path for that batch;
the client cannot select or submit an account path.

Cleanup is scoped to one lease and returns a receipt containing the original
session/device/key/lease identity, removed batch count and one of
`cleaned`, `pending`, `unknown` or `rejected`. Only `cleaned` proves deletion;
connection EOF and process termination must attempt cleanup but cannot invent a
success receipt. A separate lease can never delete another session's files.

The Node implementation exposes a separate
`--session-attachments-protocol-info` probe and the
`session_attachment_transfer` capability. Admission uses the exact route and
the actual forced-command device and SSH key; wire frames repeat
`expected_device_id` for the local session binding. The gateway derives the
account from trusted local session state, rejects account paths and legacy
receiver fallback, stages below the lease directory, and persists cleanup
receipts. Server and CLI must still be deployed and accepted together on real
SSH before a desktop release enables the capability by default.
