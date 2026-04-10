-- Dodela TRADE_STOCKS test klijentu iz 000007 (trgovanje hartijama u dev okruženju).
INSERT INTO user_permissions (user_id, permission_id)
SELECT u.id, p.id
FROM users u
JOIN permissions p ON p.permission_code = 'TRADE_STOCKS'
WHERE u.email = 'kseniakenny@gmail.com'
ON CONFLICT DO NOTHING;
