-- +goose Up
-- +goose StatementBegin

-- The transactional outbox.
--
-- The contract states that "every accepted transaction is picked up
-- asynchronously by a resolution pipeline". Publishing from the request handler
-- after the commit has an unavoidable window: the commit succeeds, the publish
-- fails, and the transaction exists while the pipeline never hears about it.
-- That is silent revenue loss, invisible until someone reconciles.
--
-- Writing the event into this table inside the same database transaction as the
-- transaction row closes the window without distributed 2PC. Either both rows
-- exist or neither does. A relay then drains the table at its own pace.
--
-- See docs/adr/0007-transactional-outbox.md.
CREATE TABLE outbox_events (
    -- BIGSERIAL rather than a UUID: the relay drains in insertion order, and a
    -- monotonic key makes "oldest unpublished" an index scan from one end.
    id              BIGSERIAL     PRIMARY KEY,

    -- A stable identifier carried in the published message so that consumers
    -- can deduplicate. Delivery is at-least-once by construction: the relay can
    -- publish and then fail before marking the row, and replaying is the safe
    -- direction to fail.
    event_id        UUID          NOT NULL UNIQUE,

    event_type      TEXT          NOT NULL,
    -- The transaction this event is about.
    aggregate_id    UUID          NOT NULL,

    payload         JSONB         NOT NULL,

    created_at      TIMESTAMPTZ   NOT NULL DEFAULT now(),

    -- NULL until the relay has successfully published. Rows are kept after
    -- publication rather than deleted: they are the audit trail for what this
    -- service told downstream and when.
    published_at    TIMESTAMPTZ,

    attempts        INT           NOT NULL DEFAULT 0,
    last_error      TEXT,
    -- Backoff: a failing publish schedules its next attempt rather than
    -- spinning. Also lets a poison message be parked far in the future without
    -- deleting it.
    next_attempt_at TIMESTAMPTZ   NOT NULL DEFAULT now(),

    CONSTRAINT outbox_events_attempts_not_negative CHECK (attempts >= 0)
);

-- The relay's only hot query: the oldest events that are due and unpublished.
-- Partial, so the index stays small as published rows accumulate — it indexes
-- the backlog, not the archive.
CREATE INDEX outbox_events_pending_idx
    ON outbox_events (next_attempt_at, id)
    WHERE published_at IS NULL;

-- "What did we publish about this transaction?", asked during a dispute.
CREATE INDEX outbox_events_aggregate_idx ON outbox_events (aggregate_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE outbox_events;
-- +goose StatementEnd
