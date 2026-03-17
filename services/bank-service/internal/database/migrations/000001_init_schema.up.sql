-- =============================================================================
-- Migration: 000001_init_schema
-- Service:   bank-service
-- Schema:    core_banking
--
-- Tables: valuta, delatnost, firma, racun
--
-- NOTE: vlasnik_id, id_zaposlenog, id_vlasnika are plain BIGINT — no FK
--       constraints. Cross-service references are resolved at the application
--       layer only.
-- =============================================================================

CREATE SCHEMA IF NOT EXISTS core_banking;

-- ─── 1. ŠIFARNIK: valuta ─────────────────────────────────────────────────────

CREATE TABLE core_banking.valuta (
    id      BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    naziv   VARCHAR(100) NOT NULL,
    oznaka  VARCHAR(10)  NOT NULL UNIQUE,   -- npr. RSD, EUR, USD
    simbol  VARCHAR(10)  NOT NULL,   -- npr. din., €, $
    zemlja  TEXT         NOT NULL,
    status  BOOLEAN      NOT NULL DEFAULT TRUE
);

-- ─── 2. ŠIFARNIK: delatnost ──────────────────────────────────────────────────

CREATE TABLE core_banking.delatnost (
    id     BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    sifra  VARCHAR(20)  NOT NULL UNIQUE,   -- npr. '1.1', '2.1', '3.1'
    naziv  VARCHAR(255) NOT NULL,
    grana  VARCHAR(100) NOT NULL,
    sektor VARCHAR(100) NOT NULL
);

-- ─── 3. firma ────────────────────────────────────────────────────────────────
-- vlasnik_id je id klijenta iz user-service-a; bez FK ograničenja.

CREATE TABLE core_banking.firma (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    naziv_firme   VARCHAR(255) NOT NULL,
    maticni_broj  VARCHAR(20)  NOT NULL,
    poreski_broj  VARCHAR(20)  NOT NULL,
    id_delatnosti BIGINT       NOT NULL REFERENCES core_banking.delatnost (id),
    adresa        VARCHAR(255) NOT NULL,
    vlasnik_id    BIGINT       NOT NULL,   -- id klijenta iz user-service; bez FK

    CONSTRAINT firma_maticni_broj_unique UNIQUE (maticni_broj),
    CONSTRAINT firma_poreski_broj_unique UNIQUE (poreski_broj)
);

-- ─── 4. racun ────────────────────────────────────────────────────────────────
-- Surogat PK je id (bigint identity); broj_racuna ostaje UNIQUE indeks.
-- id_zaposlenog i id_vlasnika su BIGINT bez FK — cross-service reference.
-- id_firme je nullable FK — popunjava se samo za poslovne račune.

CREATE TABLE core_banking.racun (
    id                   BIGINT         GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    broj_racuna          VARCHAR(18)    NOT NULL UNIQUE,
    id_zaposlenog        BIGINT         NOT NULL,                             -- zaposleni iz user-service; bez FK
    id_vlasnika          BIGINT         NOT NULL,                             -- klijent iz user-service; bez FK
    id_firme             BIGINT         REFERENCES core_banking.firma (id),   -- nullable; samo za poslovne račune
    id_valute            BIGINT         NOT NULL REFERENCES core_banking.valuta (id),
    kategorija_racuna    VARCHAR(20)    NOT NULL,
    vrsta_racuna         VARCHAR(20)    NOT NULL,
    podvrsta             VARCHAR(100),                                         -- npr. standardni, stedni; nullable
    naziv_racuna         VARCHAR(255)   NOT NULL,
    stanje_racuna        DECIMAL(15, 2) NOT NULL DEFAULT 0,
    rezervisana_sredstva DECIMAL(15, 2) NOT NULL DEFAULT 0,
    datum_kreiranja      TIMESTAMP      NOT NULL,
    datum_isteka         TIMESTAMP      NOT NULL,
    status               VARCHAR(20)    NOT NULL DEFAULT 'AKTIVAN',
    odrzavanje_racuna    DECIMAL(15, 2),
    dnevni_limit         DECIMAL(15, 2),
    mesecni_limit        DECIMAL(15, 2),
    dnevna_potrosnja     DECIMAL(15, 2) NOT NULL DEFAULT 0,
    mesecna_potrosnja    DECIMAL(15, 2) NOT NULL DEFAULT 0,

    CONSTRAINT racun_kategorija_check CHECK (kategorija_racuna IN ('TEKUCI', 'DEVIZNI')),
    CONSTRAINT racun_vrsta_check       CHECK (vrsta_racuna      IN ('LICNI', 'POSLOVNI')),
    CONSTRAINT racun_status_check      CHECK (status            IN ('AKTIVAN', 'NEAKTIVAN'))
);

CREATE INDEX idx_racun_id_vlasnika  ON core_banking.racun (id_vlasnika);
CREATE INDEX idx_racun_id_firme     ON core_banking.racun (id_firme);
CREATE INDEX idx_racun_id_valute    ON core_banking.racun (id_valute);

-- ─── Seed data ────────────────────────────────────────────────────────────────

INSERT INTO core_banking.valuta (naziv, oznaka, simbol, zemlja, status) VALUES
    ('Srpski dinar',    'RSD', 'din.', 'Srbija',           true),
    ('Evro',            'EUR', '€',    'Evropska Unija',   true),
    ('Američki dolar',  'USD', '$',    'SAD',              true)
ON CONFLICT (oznaka) DO NOTHING;

INSERT INTO core_banking.delatnost (sifra, naziv, grana, sektor) VALUES
    ('1.1', 'Centralno bankarstvo',                                'Finansijske usluge',      'Sektor K'),
    ('2.1', 'Računarsko programiranje',                            'Informacione tehnologije', 'Sektor J'),
    ('3.1', 'Trgovina na malo u nespecijalizovanim prodavnicama',  'Trgovina',                'Sektor G')
ON CONFLICT (sifra) DO NOTHING;
