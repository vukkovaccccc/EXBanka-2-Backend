UPDATE core_banking.listing
SET details_json = '{}'
WHERE listing_type = 'STOCK'
  AND ticker IN ('AAPL', 'MSFT');
