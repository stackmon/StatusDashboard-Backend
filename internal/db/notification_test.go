package db

// Storage-layer behavior for notification_outbox (Enqueue, ClaimPending,
// MarkSent, MarkFailed, RecoverStaleProcessing) is verified against a real
// Postgres container in tests/notifications_test.go, because FOR UPDATE SKIP
// LOCKED and transactional lease semantics cannot be exercised with sqlmock.
