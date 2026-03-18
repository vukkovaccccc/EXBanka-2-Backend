-- =============================================================================
-- Migration: 000002_add_transakcija
-- Service:   bank-service
-- Schema:    core_banking
--
-- Table: transakcija — zapisi o transakcijama vezanim za bankovne račune
-- =============================================================================

CREATE TABLE core_banking.transakcija (
    id                  BIGINT         GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    racun_id            BIGINT         NOT NULL REFERENCES core_banking.racun(id),
    tip_transakcije     VARCHAR(50)    NOT NULL,   -- npr. 'UPLATA' | 'ISPLATA' | 'INTERNI_TRANSFER'
    iznos               DECIMAL(15, 2) NOT NULL,
    opis                VARCHAR(500),
    vreme_izvrsavanja   TIMESTAMP      NOT NULL DEFAULT NOW(),
    status              VARCHAR(20)    NOT NULL DEFAULT 'IZVRSEN',

    CONSTRAINT transakcija_tip_check    CHECK (tip_transakcije IN ('UPLATA', 'ISPLATA', 'INTERNI_TRANSFER')),
    CONSTRAINT transakcija_status_check CHECK (status          IN ('IZVRSEN', 'CEKANJE', 'STORNIRAN'))
);

CREATE INDEX idx_transakcija_racun_id ON core_banking.transakcija (racun_id);
CREATE INDEX idx_transakcija_vreme    ON core_banking.transakcija (vreme_izvrsavanja);
