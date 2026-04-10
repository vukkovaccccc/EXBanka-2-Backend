-- approved_by: store supervisor user id as text, or the literal phrase for auto-approved orders.
ALTER TABLE core_banking.orders
    ALTER COLUMN approved_by TYPE VARCHAR(256) USING (
        CASE
            WHEN approved_by IS NULL THEN NULL
            ELSE approved_by::TEXT
        END
    );
