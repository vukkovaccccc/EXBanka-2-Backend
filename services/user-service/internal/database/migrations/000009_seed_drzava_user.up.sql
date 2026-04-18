-- =============================================================================
-- Migration: 000009_seed_drzava_user
-- Service:   user-service
--
-- Seeds the "Država" (state) system-client that receives capital-gains tax
-- payments from stock trading. Referenced by bank-service via the
-- corresponding RSD treasury account opened in bank-service/000042.
--
-- This user is a loginable CLIENT (per spec):
--   email:    drzava@exbanka.rs
--   password: Admin123  (bcrypt cost 12 — same hash as admin@raf.rs)
--
-- birth_date = 946684800000 ms = 2000-01-01 00:00:00 UTC (symbolic epoch),
-- matching the pattern from 000002_seed_trezor_user.
-- =============================================================================

INSERT INTO users (email, password_hash, salt_password, user_type, first_name, last_name, birth_date, is_active)
VALUES (
    'drzava@exbanka.rs',
    '$2a$12$AcicRLhfUC1gQ2CWY.7t0.enY/PeLQU3.whwoBNr3CwSCncnbO5Qq', -- Admin123
    '',
    'CLIENT',
    'Drzava',
    'Republika Srbija',
    946684800000,
    TRUE
)
ON CONFLICT (email) DO NOTHING;

-- client_details row is required for all CLIENT users
INSERT INTO client_details (user_id)
SELECT id FROM users WHERE email = 'drzava@exbanka.rs'
ON CONFLICT (user_id) DO NOTHING;
