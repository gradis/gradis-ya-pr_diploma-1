BEGIN;

ALTER TABLE users ALTER COLUMN password_hash TYPE VARCHAR(60);

-- Preserve the byte limit for Unicode logins; VARCHAR limits characters.
ALTER TABLE users ALTER COLUMN login TYPE VARCHAR(256);
ALTER TABLE orders ALTER COLUMN number TYPE VARCHAR(256);
ALTER TABLE withdrawals ALTER COLUMN order_number TYPE VARCHAR(256);
ALTER TABLE orders DROP CONSTRAINT orders_number_check;
ALTER TABLE orders ADD CONSTRAINT orders_number_check CHECK (number ~ '^[0-9]+$');
ALTER TABLE withdrawals DROP CONSTRAINT withdrawals_order_number_check;
ALTER TABLE withdrawals ADD CONSTRAINT withdrawals_order_number_check CHECK (order_number ~ '^[0-9]+$');

CREATE TYPE order_status AS ENUM ('NEW', 'PROCESSING', 'INVALID', 'PROCESSED');
DROP INDEX orders_pending_idx;
ALTER TABLE orders DROP CONSTRAINT orders_status_check;
ALTER TABLE orders ALTER COLUMN status DROP DEFAULT;
ALTER TABLE orders ALTER COLUMN status TYPE order_status USING status::order_status;
ALTER TABLE orders ALTER COLUMN status SET DEFAULT 'NEW'::order_status;
CREATE INDEX orders_pending_idx ON orders (next_check_at)
    WHERE status IN ('NEW', 'PROCESSING');

COMMIT;
