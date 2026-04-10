-- =============================================================================
-- Migration: 000033_seed_more_listings
-- Service:   bank-service
-- Schema:    core_banking
--
-- Dodaje veliki broj hartija od vrednosti radi realnijeg testiranja:
--   STOCKS:  20 popularnih akcija (NASDAQ i NYSE)
--   FOREX:   7 dodatnih valutnih parova
--   FUTURES: 7 novih terminskih ugovora sa budućim settlement datumima
--   OPTIONS: matrica opcija za MSFT
--
-- Napomena: existing futures CLJ26 i SIH26 imaju prošle settlement datume
-- (apr/mar 2026). Ažuriramo ih na buduće datume da omogućimo testiranje.
-- =============================================================================

-- ─── Ispravi settlement datume za postojeće futures ──────────────────────────

UPDATE core_banking.listing
SET details_json = jsonb_set(details_json::jsonb, '{settlement_date}', '"2026-12-31"')::text
WHERE ticker = 'CLJ26' AND listing_type = 'FUTURE';

UPDATE core_banking.listing
SET details_json = jsonb_set(details_json::jsonb, '{settlement_date}', '"2026-11-28"')::text
WHERE ticker = 'SIH26' AND listing_type = 'FUTURE';

-- ─── STOCKS — NASDAQ (XNAS) ───────────────────────────────────────────────────

INSERT INTO core_banking.listing (ticker, name, exchange_id, listing_type, price, ask, bid, volume, details_json)
SELECT
    t.ticker,
    t.name,
    e.id AS exchange_id,
    'STOCK'::VARCHAR AS listing_type,
    t.price,
    t.ask,
    t.bid,
    t.volume,
    t.details_json::TEXT
FROM (VALUES
    ('GOOG',  'Alphabet Inc.',              170.00, 170.20, 169.80, 18000000, '{"outstanding_shares":12220000000,"dividend_yield":0}'),
    ('AMZN',  'Amazon.com Inc.',            185.00, 185.25, 184.75, 35000000, '{"outstanding_shares":10640000000,"dividend_yield":0}'),
    ('NVDA',  'NVIDIA Corporation',         875.00, 875.50, 874.50, 42000000, '{"outstanding_shares":24400000000,"dividend_yield":0.0003}'),
    ('TSLA',  'Tesla Inc.',                 175.00, 175.30, 174.70, 90000000, '{"outstanding_shares":3190000000,"dividend_yield":0}'),
    ('META',  'Meta Platforms Inc.',        490.00, 490.40, 489.60, 14000000, '{"outstanding_shares":2200000000,"dividend_yield":0.004}'),
    ('NFLX',  'Netflix Inc.',               620.00, 620.60, 619.40, 4000000,  '{"outstanding_shares":430000000,"dividend_yield":0}'),
    ('AMD',   'Advanced Micro Devices',     165.00, 165.20, 164.80, 45000000, '{"outstanding_shares":1620000000,"dividend_yield":0}'),
    ('INTC',  'Intel Corporation',           32.00,  32.05,  31.95, 40000000, '{"outstanding_shares":4240000000,"dividend_yield":0.0125}'),
    ('ORCL',  'Oracle Corporation',         125.00, 125.15, 124.85, 8000000,  '{"outstanding_shares":2750000000,"dividend_yield":0.0128}'),
    ('ADBE',  'Adobe Inc.',                 450.00, 450.45, 449.55, 3500000,  '{"outstanding_shares":450000000,"dividend_yield":0}'),
    ('CRM',   'Salesforce Inc.',            290.00, 290.30, 289.70, 5000000,  '{"outstanding_shares":970000000,"dividend_yield":0}'),
    ('PYPL',  'PayPal Holdings Inc.',        65.00,  65.07,  64.93, 10000000, '{"outstanding_shares":1080000000,"dividend_yield":0}'),
    ('QCOM',  'Qualcomm Inc.',              165.00, 165.18, 164.82, 7000000,  '{"outstanding_shares":1110000000,"dividend_yield":0.02}'),
    ('BKNG',  'Booking Holdings Inc.',     3800.00,3802.00,3798.00,  800000,  '{"outstanding_shares":40000000,"dividend_yield":0}')
) AS t(ticker, name, price, ask, bid, volume, details_json)
CROSS JOIN (
    SELECT id FROM core_banking.exchange WHERE mic_code = 'XNAS' LIMIT 1
) e
ON CONFLICT (ticker) DO NOTHING;

-- ─── STOCKS — NYSE (XNYS) ─────────────────────────────────────────────────────

INSERT INTO core_banking.listing (ticker, name, exchange_id, listing_type, price, ask, bid, volume, details_json)
SELECT
    t.ticker,
    t.name,
    e.id AS exchange_id,
    'STOCK'::VARCHAR AS listing_type,
    t.price,
    t.ask,
    t.bid,
    t.volume,
    t.details_json::TEXT
FROM (VALUES
    ('JPM',   'JPMorgan Chase & Co.',       200.00, 200.20, 199.80, 12000000, '{"outstanding_shares":2890000000,"dividend_yield":0.024}'),
    ('BAC',   'Bank of America Corp.',       38.00,  38.04,  37.96, 30000000, '{"outstanding_shares":7900000000,"dividend_yield":0.027}'),
    ('V',     'Visa Inc.',                  280.00, 280.28, 279.72, 6000000,  '{"outstanding_shares":2070000000,"dividend_yield":0.008}'),
    ('JNJ',   'Johnson & Johnson',          155.00, 155.15, 154.85, 7000000,  '{"outstanding_shares":2410000000,"dividend_yield":0.031}'),
    ('XOM',   'Exxon Mobil Corporation',    110.00, 110.11, 109.89, 14000000, '{"outstanding_shares":4070000000,"dividend_yield":0.034}'),
    ('WMT',   'Walmart Inc.',                65.00,  65.07,  64.93, 10000000, '{"outstanding_shares":8040000000,"dividend_yield":0.01}'),
    ('DIS',   'The Walt Disney Company',     95.00,  95.10,  94.90, 8000000,  '{"outstanding_shares":1840000000,"dividend_yield":0}'),
    ('GS',    'Goldman Sachs Group Inc.',   490.00, 490.49, 489.51, 3000000,  '{"outstanding_shares":310000000,"dividend_yield":0.024}'),
    ('HD',    'Home Depot Inc.',            340.00, 340.34, 339.66, 4000000,  '{"outstanding_shares":1020000000,"dividend_yield":0.023}'),
    ('PG',    'Procter & Gamble Co.',       165.00, 165.17, 164.83, 6000000,  '{"outstanding_shares":2360000000,"dividend_yield":0.024}'),
    ('KO',    'Coca-Cola Company',           62.00,  62.06,  61.94, 12000000, '{"outstanding_shares":4310000000,"dividend_yield":0.03}'),
    ('MRK',   'Merck & Co. Inc.',            98.00,  98.10,  97.90, 9000000,  '{"outstanding_shares":2540000000,"dividend_yield":0.033}')
) AS t(ticker, name, price, ask, bid, volume, details_json)
CROSS JOIN (
    SELECT id FROM core_banking.exchange WHERE mic_code = 'XNYS' LIMIT 1
) e
ON CONFLICT (ticker) DO NOTHING;

-- ─── FOREX — dodatni valutni parovi (XNAS kao referentna berza) ──────────────

INSERT INTO core_banking.listing (ticker, name, exchange_id, listing_type, price, ask, bid, volume, details_json)
SELECT
    t.ticker,
    t.name,
    e.id AS exchange_id,
    'FOREX'::VARCHAR AS listing_type,
    t.price,
    t.ask,
    t.bid,
    t.volume,
    t.details_json::TEXT
FROM (VALUES
    ('USD/JPY', 'US Dollar / Japanese Yen',      151.50, 151.58, 151.42, 8000000,
     '{"base_currency":"USD","quote_currency":"JPY","contract_size":1000,"liquidity":"High"}'),
    ('EUR/GBP', 'Euro / British Pound',            0.8560,  0.8563,  0.8557, 4000000,
     '{"base_currency":"EUR","quote_currency":"GBP","contract_size":1000,"liquidity":"High"}'),
    ('AUD/USD', 'Australian Dollar / US Dollar',   0.6450,  0.6453,  0.6447, 3500000,
     '{"base_currency":"AUD","quote_currency":"USD","contract_size":1000,"liquidity":"High"}'),
    ('USD/CHF', 'US Dollar / Swiss Franc',         0.9020,  0.9024,  0.9016, 3000000,
     '{"base_currency":"USD","quote_currency":"CHF","contract_size":1000,"liquidity":"High"}'),
    ('USD/CAD', 'US Dollar / Canadian Dollar',     1.3600,  1.3604,  1.3596, 3500000,
     '{"base_currency":"USD","quote_currency":"CAD","contract_size":1000,"liquidity":"High"}'),
    ('EUR/JPY', 'Euro / Japanese Yen',           163.80, 163.88, 163.72, 3000000,
     '{"base_currency":"EUR","quote_currency":"JPY","contract_size":1000,"liquidity":"Medium"}'),
    ('GBP/JPY', 'British Pound / Japanese Yen',  191.40, 191.50, 191.30, 2000000,
     '{"base_currency":"GBP","quote_currency":"JPY","contract_size":1000,"liquidity":"Medium"}')
) AS t(ticker, name, price, ask, bid, volume, details_json)
CROSS JOIN (
    SELECT id FROM core_banking.exchange WHERE mic_code = 'XNAS' LIMIT 1
) e
ON CONFLICT (ticker) DO NOTHING;

-- ─── FUTURES — novi sa budućim settlement datumima (XCBO) ─────────────────────

INSERT INTO core_banking.listing (ticker, name, exchange_id, listing_type, price, ask, bid, volume, details_json)
SELECT
    t.ticker,
    t.name,
    e.id AS exchange_id,
    'FUTURE'::VARCHAR AS listing_type,
    t.price,
    t.ask,
    t.bid,
    t.volume,
    t.details_json::TEXT
FROM (VALUES
    ('CLK26', 'Crude Oil Futures (May 2026)',       72.10, 72.18, 72.02, 900000,
     '{"contract_size":1000,"contract_unit":"Barrel","settlement_date":"2026-05-20"}'),
    ('GCK26', 'Gold Futures (May 2026)',           3150.0,3151.5,3148.5, 250000,
     '{"contract_size":100,"contract_unit":"Troy Ounce","settlement_date":"2026-05-28"}'),
    ('SIK26', 'Silver Futures (May 2026)',            32.5,  32.55,  32.45, 180000,
     '{"contract_size":5000,"contract_unit":"Troy Ounce","settlement_date":"2026-05-28"}'),
    ('NGK26', 'Natural Gas Futures (May 2026)',       2.85,   2.86,   2.84, 320000,
     '{"contract_size":10000,"contract_unit":"MMBtu","settlement_date":"2026-05-27"}'),
    ('ZCK26', 'Corn Futures (May 2026)',             465.0,  465.5,  464.5, 200000,
     '{"contract_size":5000,"contract_unit":"Bushel","settlement_date":"2026-05-14"}'),
    ('CLM26', 'Crude Oil Futures (Jun 2026)',         72.00,  72.08,  71.92, 600000,
     '{"contract_size":1000,"contract_unit":"Barrel","settlement_date":"2026-06-22"}'),
    ('GCM26', 'Gold Futures (Jun 2026)',            3155.0, 3156.5, 3153.5, 200000,
     '{"contract_size":100,"contract_unit":"Troy Ounce","settlement_date":"2026-06-26"}')
) AS t(ticker, name, price, ask, bid, volume, details_json)
CROSS JOIN (
    SELECT id FROM core_banking.exchange WHERE mic_code = 'XCBO' LIMIT 1
) e
ON CONFLICT (ticker) DO NOTHING;

-- ─── OPTIONS za MSFT ──────────────────────────────────────────────────────────

UPDATE core_banking.listing
SET details_json = '{
  "options": [
    {"strike":390,"callBid":32.50,"callAsk":33.00,"callVol":2800,"callOI":14500,"putBid":0.95,"putAsk":1.15,"putVol":600,"putOI":4200},
    {"strike":395,"callBid":27.80,"callAsk":28.30,"callVol":3500,"callOI":18200,"putBid":1.40,"putAsk":1.65,"putVol":850,"putOI":6100},
    {"strike":400,"callBid":23.30,"callAsk":23.80,"callVol":5100,"callOI":22800,"putBid":2.10,"putAsk":2.40,"putVol":1200,"putOI":8900},
    {"strike":405,"callBid":19.10,"callAsk":19.60,"callVol":6800,"callOI":27500,"putBid":3.20,"putAsk":3.55,"putVol":1700,"putOI":11600},
    {"strike":410,"callBid":15.20,"callAsk":15.70,"callVol":8900,"callOI":32000,"putBid":4.80,"putAsk":5.20,"putVol":2300,"putOI":14800},
    {"strike":415,"callBid":11.60,"callAsk":12.10,"callVol":7400,"callOI":27000,"putBid":7.10,"putAsk":7.50,"putVol":1900,"putOI":12200},
    {"strike":420,"callBid":8.40,"callAsk":8.80,"callVol":5900,"callOI":21500,"putBid":9.90,"putAsk":10.40,"putVol":1400,"putOI":9500},
    {"strike":425,"callBid":5.80,"callAsk":6.20,"callVol":4200,"callOI":16000,"putBid":13.20,"putAsk":13.70,"putVol":980,"putOI":7000},
    {"strike":430,"callBid":3.70,"callAsk":4.10,"callVol":2800,"callOI":11200,"putBid":17.00,"putAsk":17.60,"putVol":620,"putOI":4800}
  ]
}'
WHERE ticker = 'MSFT';
