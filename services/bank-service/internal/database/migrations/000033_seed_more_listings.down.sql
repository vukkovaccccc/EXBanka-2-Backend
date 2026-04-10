-- 000033_seed_more_listings.down.sql
-- Uklanja hartije dodate u up migraciji i vraća options za AAPL/MSFT na {}

-- Ukloni MSFT options matricu
UPDATE core_banking.listing SET details_json = '{}' WHERE ticker = 'MSFT';

-- Ukloni futures
DELETE FROM core_banking.listing
WHERE ticker IN ('CLK26','GCK26','SIK26','NGK26','ZCK26','CLM26','GCM26')
  AND listing_type = 'FUTURE';

-- Ukloni forex
DELETE FROM core_banking.listing
WHERE ticker IN ('USD/JPY','EUR/GBP','AUD/USD','USD/CHF','USD/CAD','EUR/JPY','GBP/JPY')
  AND listing_type = 'FOREX';

-- Ukloni NASDAQ stocks
DELETE FROM core_banking.listing
WHERE ticker IN ('GOOG','AMZN','NVDA','TSLA','META','NFLX','AMD','INTC','ORCL','ADBE','CRM','PYPL','QCOM','BKNG')
  AND listing_type = 'STOCK';

-- Ukloni NYSE stocks
DELETE FROM core_banking.listing
WHERE ticker IN ('JPM','BAC','V','JNJ','XOM','WMT','DIS','GS','HD','PG','KO','MRK')
  AND listing_type = 'STOCK';
