-- warrant's tables. Applied at startup; every statement is idempotent.

CREATE TABLE IF NOT EXISTS users (
    id          text PRIMARY KEY,
    name        text NOT NULL,
    device_key  text NOT NULL,          -- base64 Ed25519, held by the user's UPI app
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS agents (
    id          text PRIMARY KEY,
    name        text NOT NULL,
    developer   text NOT NULL,
    key         text NOT NULL,
    status      text NOT NULL DEFAULT 'active',   -- active, suspended
    -- set when the developer reports a leaked key: debits signed after this
    -- moment are on the developer
    compromised_since timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS merchants (
    id          text PRIMARY KEY,
    name        text NOT NULL,
    vpa         text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS mandates (
    id          text PRIMARY KEY,
    user_id     text NOT NULL REFERENCES users,
    agent_id    text NOT NULL REFERENCES agents,
    kind        text NOT NULL,
    scope       jsonb NOT NULL,          -- the user's signed envelope, as received
    block_ref   text NOT NULL,           -- the issuer's block or delegation
    status      text NOT NULL DEFAULT 'active',   -- active, revoked
    created_at  timestamptz NOT NULL,
    expires_at  timestamptz NOT NULL,
    revoked_at  timestamptz
);
CREATE INDEX IF NOT EXISTS mandates_user ON mandates (user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS requests (
    id          text PRIMARY KEY,
    agent_id    text NOT NULL,
    mandate_id  text NOT NULL,
    idem_key    text NOT NULL,
    nonce       text NOT NULL,
    digest      text NOT NULL,           -- SHA-256 of the signed payload
    envelope    jsonb NOT NULL,
    intent      jsonb,
    input       jsonb NOT NULL,          -- what the policy saw
    decision    jsonb NOT NULL,
    outcome     text NOT NULL,           -- approve, step_up, deny
    created_at  timestamptz NOT NULL,
    UNIQUE (agent_id, idem_key),
    UNIQUE (agent_id, nonce)
);
CREATE INDEX IF NOT EXISTS requests_mandate ON requests (mandate_id, created_at);

CREATE TABLE IF NOT EXISTS stepups (
    id          text PRIMARY KEY,
    request_id  text NOT NULL UNIQUE REFERENCES requests,
    user_id     text NOT NULL,
    status      text NOT NULL DEFAULT 'pending',  -- pending, approved, declined, expired
    answer      jsonb,
    created_at  timestamptz NOT NULL,
    expires_at  timestamptz NOT NULL,
    decided_at  timestamptz
);
CREATE INDEX IF NOT EXISTS stepups_pending ON stepups (user_id) WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS debits (
    id          text PRIMARY KEY,        -- also the transaction ID at the bank
    request_id  text NOT NULL UNIQUE REFERENCES requests,
    mandate_id  text NOT NULL,
    merchant    text NOT NULL,
    amount      bigint NOT NULL,
    -- pending: counted against the limit, not yet sent
    -- unknown: sent, no answer yet; the reconciler asks the bank
    status      text NOT NULL,           -- pending, unknown, succeeded, failed
    attempts    int NOT NULL DEFAULT 0,
    last_error  text,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    settled_at  timestamptz
);
CREATE INDEX IF NOT EXISTS debits_mandate ON debits (mandate_id, created_at);
CREATE INDEX IF NOT EXISTS debits_open ON debits (updated_at) WHERE status IN ('pending', 'unknown');

-- The evidence log. Records land here unsequenced in the transaction that
-- creates them; the sequencer gives them a place in the Merkle tree.
CREATE TABLE IF NOT EXISTS entries (
    id          bigserial PRIMARY KEY,
    idx         bigint UNIQUE,           -- leaf index, null until sequenced
    kind        text NOT NULL,
    subject     text NOT NULL,
    body        bytea NOT NULL,          -- the exact bytes that were hashed
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS entries_subject ON entries (subject);
CREATE INDEX IF NOT EXISTS entries_unsequenced ON entries (id) WHERE idx IS NULL;

CREATE TABLE IF NOT EXISTS checkpoints (
    size        bigint PRIMARY KEY,
    envelope    jsonb NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS disputes (
    id          text PRIMARY KEY,
    debit_id    text NOT NULL REFERENCES debits,
    claim       text NOT NULL,
    ruling      jsonb NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Events for other systems, written in the same transaction as the change
-- they describe and relayed to Kafka when a broker is configured.
CREATE TABLE IF NOT EXISTS outbox (
    id          bigserial PRIMARY KEY,
    topic       text NOT NULL,
    key         text NOT NULL,
    payload     jsonb NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    sent_at     timestamptz
);
CREATE INDEX IF NOT EXISTS outbox_unsent ON outbox (id) WHERE sent_at IS NULL;
