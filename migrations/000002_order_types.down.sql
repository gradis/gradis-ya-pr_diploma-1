BEGIN;

ALTER TABLE users ALTER COLUMN password_hash TYPE TEXT;

DROP INDEX orders_pending_idx;
ALTER TABLE orders ALTER COLUMN status DROP DEFAULT;
ALTER TABLE orders ALTER COLUMN status TYPE TEXT USING status::text;
ALTER TABLE orders ALTER COLUMN status SET DEFAULT 'NEW';
ALTER TABLE orders ADD CONSTRAINT orders_status_check
    CHECK (status IN ('NEW', 'PROCESSING', 'INVALID', 'PROCESSED'));
CREATE INDEX orders_pending_idx ON orders (next_check_at)
    WHERE status IN ('NEW', 'PROCESSING');
DROP TYPE order_status;

ALTER TABLE users ALTER COLUMN login TYPE TEXT;
ALTER TABLE orders ALTER COLUMN number TYPE TEXT;
ALTER TABLE withdrawals ALTER COLUMN order_number TYPE TEXT;
ALTER TABLE orders DROP CONSTRAINT orders_number_check;
ALTER TABLE orders ADD CONSTRAINT orders_number_check
    CHECK (number ~ '^[0-9]+$' AND length(number) <= 256);
ALTER TABLE withdrawals DROP CONSTRAINT withdrawals_order_number_check;
ALTER TABLE withdrawals ADD CONSTRAINT withdrawals_order_number_check
    CHECK (order_number ~ '^[0-9]+$' AND length(order_number) <= 256);

COMMIT;
