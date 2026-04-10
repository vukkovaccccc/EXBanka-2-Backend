ALTER TABLE core_banking.orders
    ALTER COLUMN approved_by TYPE BIGINT USING (
        CASE
            WHEN approved_by IS NULL OR approved_by = '' THEN NULL
            WHEN approved_by ~ '^[0-9]+$' THEN approved_by::BIGINT
            ELSE NULL
        END
    );
