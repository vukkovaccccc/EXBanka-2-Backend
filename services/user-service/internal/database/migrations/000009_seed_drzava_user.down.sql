-- =============================================================================
-- Migration: 000009_seed_drzava_user (DOWN)
-- =============================================================================

DELETE FROM user_permissions
WHERE user_id = (SELECT id FROM users WHERE email = 'drzava@exbanka.rs');

DELETE FROM client_details
WHERE user_id = (SELECT id FROM users WHERE email = 'drzava@exbanka.rs');

DELETE FROM users WHERE email = 'drzava@exbanka.rs';
