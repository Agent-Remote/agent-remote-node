# Scoped frozen-object readers

Large finalizations require one validated manifest index per bounded read lifetime. Re-reading the
complete session baseline and frozen manifest for every distinct object is quadratic in entry count.
The `read_skill_finalization_objects` Helper operation pins one exact original Native capture and
holds the existing manifest kernel read lock throughout a connection of at most 15 minutes. It
validates the complete binding, private session receipt, manifest and content state before opening
an object directory anchor. The initial read-only manifest descriptor also transfers that hold to the
client, so Helper restart cannot release protection while the client still consumes a received object.

The connection serially requests only manifest-member digests. Each response sends one read-only,
close-on-exec, ordinary single-link object descriptor with its original entry and capture identity.
Unknown digests, changed identities, malformed frames, additional fields, missing acknowledgements,
private metadata mutation and cancellation terminate the reader. The manifest/object directory
anchors and their original metadata remain checked; no untrusted path or new capture is accepted.
Each object retains its own kernel read lock. No persistent cache or cross-connection token is used.

Worker uploads and SSH frozen exports use the scoped reader for their complete object loop. Their
existing content digest/classification checks, Server authorization and export reauthorization remain
required. Connection failure cannot silently fall back to another input or recapture work. Existing
single-file descriptor calls retain their compatibility semantics. Reader closure releases only its
read hold; it cannot acknowledge remote persistence, publish account state or authorize reclamation.

The initial authenticated request payload has exactly one `capture` field containing the complete
original finalization record. Its first response is the existing strict version-1 descriptor frame
with kind `manifest`, exact record, size and null entry. Subsequent requests use the existing object
selector fields (`binding`, `kind`, `digest`, `tree_digest`, `unclean`) on the same socket. Each response
uses the existing object metadata and one SCM_RIGHTS descriptor, followed by the existing bounded
single-byte acknowledgement. At most 100,000 object requests are accepted, including repeats.
The earlier caller deadline always wins. The lifecycle mutex is released after initial validation;
read lifetime exclusion comes from kernel locks. Callers close every returned object and the reader.

Actual Linux ARM64 acceptance used 100,000 unique files with 2 CPUs/2 GiB, unchanged default limits,
full capture and hashing of every object returned through the reader. It passed in 126.91 seconds,
including 88.556 seconds for socket reconciliation and 31.298 seconds for the complete descriptor
loop. This is Helper IPC capacity, not Server/HTTP/SSH capacity at that scale.

Linux tests cover unknown/foreign objects, metadata and link substitution, reclamation exclusion,
read-only descriptors, manifest holds surviving Helper shutdown, malformed ancillary/JSON frames
with descriptor-leak checks, initial-lock cancellation, and cancellation/deadlines during blocked
object reads. Both real CLI/SSH export cases pass (frozen and runtime-quota recovery). The shared
systemd fixture waits for the expected executable and UID because Type=simple start completion can
precede child credential setup. This fixture repair changes no shipped service behavior.

The complete synthetic CLI lifecycle passed on two subsequent runs (132.47 and 127.14 seconds),
including publication, daemon restart, independent-session inheritance, reclamation and deletion.
An earlier run returned unclean/detached at stop; its intermittent cause remains unresolved and the
successful retries do not close that issue. The test tool records its received terminal byte for
future diagnosis. Logs: `/tmp/skill-reader-cli-lifecycle.log`,
`/tmp/skill-reader-cli-lifecycle-diagnostic.log`, `/tmp/skill-reader-cli-lifecycle-monitored.log`.

A complete HTTP upload can outlive one bounded Helper connection. Worker checks the reader age
between files and obtains a new reader for the exact same capture after ten minutes. The replacement
validates/pins that original input before the old client hold is closed. The outer complete-upload
hold remains in force throughout. A single slow file keeps its transferred FD even if the socket
expires; the next file uses a replacement connection. Failure preserves the original transfer input
and never acknowledges completion. This does not renew Server upload leases or extend SSH export's
separate overall deadline.
