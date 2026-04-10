-- =============================================================================
-- Migration: 000006_add_missing_permissions
-- Service:   user-service
--
-- Adds permission codes that are referenced in the frontend but were missing
-- from the codebook:
--
--   TRADE_STOCKS — dozvola za klijenta da pristupi sekciji Berze/trading.
--                  Dodaje se u codebook radi konzistentnosti; u praksi se
--                  dodela klijentu: user-service GET/PATCH /client/{id}/trade-permission (zaposleni) + UI na detalju klijenta,
--                  pa se frontend prikazuje bez uslova permisije za sve klijente.
--
--   MANAGE_USERS — admin-nivo dozvola za upravljanje zaposlenima; referencira
--                  se u Sidebar-u ali ADMIN korisnici uvek prolaze hasPermission,
--                  pa ovo ne menja ponašanje — samo sinhronizuje codebook sa
--                  frontend referencama.
-- =============================================================================

INSERT INTO permissions (permission_code) VALUES
    ('TRADE_STOCKS'),   -- klijentski pristup sekciji Berze / trading
    ('MANAGE_USERS')    -- admin upravljanje zaposlenima (referencira frontend)
ON CONFLICT (permission_code) DO NOTHING;
