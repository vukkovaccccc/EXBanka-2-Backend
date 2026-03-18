-- 000003_add_pending_action.up.sql
-- Tabela za verifikaciju zahteva koji se odobravaju putem mobilne aplikacije.
-- Trenutno podržava: PROMENA_LIMITA

CREATE TABLE IF NOT EXISTS core_banking.pending_action (
    id                  BIGSERIAL PRIMARY KEY,
    vlasnik_id          BIGINT      NOT NULL,
    racun_id            BIGINT      NOT NULL REFERENCES core_banking.racun(id),
    action_type         VARCHAR(50) NOT NULL
        CONSTRAINT pending_action_type_check CHECK (action_type IN ('PROMENA_LIMITA')),
    -- JSON params specifični za tip akcije:
    --   PROMENA_LIMITA: { "dnevni_limit": 5000.0, "mesecni_limit": 50000.0 }
    params_json         JSONB       NOT NULL DEFAULT '{}',
    opis                TEXT        NOT NULL DEFAULT '',
    status              VARCHAR(20) NOT NULL DEFAULT 'PENDING'
        CONSTRAINT pending_action_status_check CHECK (status IN ('PENDING', 'APPROVED', 'EXPIRED', 'CANCELLED')),
    verification_code   VARCHAR(6),
    code_expires_at     TIMESTAMPTZ,
    attempts            INT         NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pending_action_vlasnik_status
    ON core_banking.pending_action (vlasnik_id, status);
