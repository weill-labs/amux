// Package mailbox defines the server-owned pane mailbox data model and
// in-memory store.
//
// Store is intentionally not synchronized. The amux server should mutate a
// session's Store from the session event loop so command handlers, waiters,
// events, and later checkpointing code all observe one ordered state.
package mailbox
