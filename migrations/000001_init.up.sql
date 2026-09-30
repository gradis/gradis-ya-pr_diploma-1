CREATE TABLE users (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    login TEXT NOT NULL UNIQUE CHECK (login <> '' AND octet_length(login) <= 256),
    password_hash TEXT NOT NULL CHECK (password_hash <> ''),
    balance_cents BIGINT NOT NULL DEFAULT 0 CHECK (balance_cents >= 0),
    withdrawn_cents BIGINT NOT NULL DEFAULT 0 CHECK (withdrawn_cents >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE orders (
    number TEXT PRIMARY KEY CHECK (number ~ '^[0-9]+$' AND length(number) <= 256),
    user_id BIGINT NOT NULL REFERENCES users(id),
    status TEXT NOT NULL DEFAULT 'NEW'
        CHECK (status IN ('NEW', 'PROCESSING', 'INVALID', 'PROCESSED')),
    accrual_cents BIGINT CHECK (accrual_cents IS NULL OR accrual_cents >= 0),
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    next_check_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX orders_user_uploaded_idx ON orders (user_id, uploaded_at DESC);
CREATE INDEX orders_pending_idx ON orders (next_check_at)
    WHERE status IN ('NEW', 'PROCESSING');

CREATE TABLE withdrawals (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    order_number TEXT NOT NULL UNIQUE
        CHECK (order_number ~ '^[0-9]+$' AND length(order_number) <= 256),
    sum_cents BIGINT NOT NULL CHECK (sum_cents > 0),
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX withdrawals_user_processed_idx
    ON withdrawals (user_id, processed_at DESC);
