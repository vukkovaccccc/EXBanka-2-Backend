DELETE FROM user_permissions
WHERE permission_id = (SELECT id FROM permissions WHERE permission_code = 'TRADE_STOCKS')
  AND user_id = (SELECT id FROM users WHERE email = 'kseniakenny@gmail.com');
