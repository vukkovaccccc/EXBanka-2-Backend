-- =============================================================================
-- Migration: 000004_add_payment_tables
-- Service:   bank-service
-- Schema:    core_banking
--
-- Dodaje tabele za plaćanja i primaoce plaćanja.
-- Verifikacija plaćanja koristi postojeću tabelu pending_action uz proširenje
-- dozvoljenih tipova akcija.
--
-- Idempotentnost: payment_intent.idempotency_key UNIQUE garantuje da isti
-- zahtev ne može biti izvršen dva puta čak i uz retry/refresh scenarije.
--
-- Race condition zaštita: izvršenje plaćanja koristi SELECT FOR UPDATE nad
-- racun redovima u determinističkom redosledu (ORDER BY id), čime se sprečava
-- deadlock pri konkurentnim zahtevima.
-- =============================================================================

-- ─── 1. Proširiti pending_action tip akcija ───────────────────────────────────
-- Dodajemo PLACANJE i PRENOS uz postojeći PROMENA_LIMITA.
ALTER TABLE core_banking.pending_action
    DROP CONSTRAINT IF EXISTS pending_action_type_check,
    ADD CONSTRAINT pending_action_type_check
        CHECK (action_type IN ('PROMENA_LIMITA', 'PLACANJE', 'PRENOS'));

-- ─── 2. payment_recipient — primaoci plaćanja ────────────────────────────────

CREATE TABLE IF NOT EXISTS core_banking.payment_recipient (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    vlasnik_id  BIGINT       NOT NULL,    -- id klijenta iz user-service; bez FK
    naziv       VARCHAR(255) NOT NULL,
    broj_racuna VARCHAR(18)  NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    -- Jedan vlasnik ne može imati duplikat istog broja računa u primaocima.
    CONSTRAINT payment_recipient_vlasnik_racun_unique UNIQUE (vlasnik_id, broj_racuna)
);

CREATE INDEX IF NOT EXISTS idx_payment_recipient_vlasnik
    ON core_banking.payment_recipient (vlasnik_id);

-- ─── 3. payment_intent — nalozi plaćanja i prenosa ───────────────────────────
-- Čuva pun kontekst svakog plaćanja/prenosa od inicijacije do izvršenja.
-- Verifikacioni tok delegira se na pending_action tabelu (pending_action_id).

CREATE TABLE IF NOT EXISTS core_banking.payment_intent (
    id                      BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,

    -- Idempotentnost: klijent generiše UUID pre slanja; isti UUID → isti rezultat.
    idempotency_key         UUID         NOT NULL,

    -- Generisani interni broj naloga (čitljiv identifikator za korisnika).
    broj_naloga             VARCHAR(30)  NOT NULL,

    -- Tip: PLACANJE (između klijenata) ili PRENOS (isti klijent, različiti računi).
    tip_transakcije         VARCHAR(20)  NOT NULL
        CONSTRAINT pi_tip_check CHECK (tip_transakcije IN ('PLACANJE', 'PRENOS')),

    -- Račun platioca (FK na racun, uz row locking pri izvršenju).
    racun_platioca_id       BIGINT       NOT NULL REFERENCES core_banking.racun(id),
    broj_racuna_platioca    VARCHAR(18)  NOT NULL,

    -- Račun primaoca: za interni prenos postoji FK, za eksterno plaćanje null.
    racun_primaoca_id       BIGINT       REFERENCES core_banking.racun(id),
    broj_racuna_primaoca    VARCHAR(18)  NOT NULL,
    naziv_primaoca          VARCHAR(255) NOT NULL,

    -- Finansijski podaci.
    iznos                   DECIMAL(15,4) NOT NULL,
    krajnji_iznos           DECIMAL(15,4),
    provizija               DECIMAL(15,4) NOT NULL DEFAULT 0,
    valuta                  VARCHAR(10)   NOT NULL,

    -- Podaci plaćanja.
    sifra_placanja          VARCHAR(3),   -- 3 cifre, počinje sa 2 za online plaćanja
    poziv_na_broj           VARCHAR(50),
    svrha_placanja          VARCHAR(500),

    -- Statusi.
    status                  VARCHAR(20)   NOT NULL DEFAULT 'U_OBRADI'
        CONSTRAINT pi_status_check CHECK (status IN ('U_OBRADI', 'REALIZOVANO', 'ODBIJENO')),

    -- Verifikacioni tok putem pending_action.
    pending_action_id       BIGINT        REFERENCES core_banking.pending_action(id),

    -- Audit polja.
    initiated_by_user_id    BIGINT        NOT NULL,
    created_at              TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    verified_at             TIMESTAMPTZ,
    executed_at             TIMESTAMPTZ,
    failed_reason           TEXT,

    -- Idempotentnost na nivou baze: isti ključ → isti zapis.
    CONSTRAINT pi_idempotency_unique UNIQUE (idempotency_key)
);

-- Indeksi za brzo pretraživanje.
CREATE INDEX IF NOT EXISTS idx_payment_intent_user
    ON core_banking.payment_intent (initiated_by_user_id);

CREATE INDEX IF NOT EXISTS idx_payment_intent_status
    ON core_banking.payment_intent (status);

CREATE INDEX IF NOT EXISTS idx_payment_intent_created_at
    ON core_banking.payment_intent (created_at DESC);

CREATE INDEX IF NOT EXISTS idx_payment_intent_racun_platioca
    ON core_banking.payment_intent (racun_platioca_id);

CREATE INDEX IF NOT EXISTS idx_payment_intent_idempotency
    ON core_banking.payment_intent (idempotency_key);
