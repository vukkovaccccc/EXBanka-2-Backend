-- =============================================================================
-- Migration: 000038_cleanup_excess_options
-- Service:   bank-service
-- Schema:    core_banking
--
-- Briše sve opcije čiji underlying nije u top 5 stockova po volumenu.
-- Worker pri sledećem pokretanju regeneriše opcije samo za top 5.
-- =============================================================================

DELETE FROM core_banking.listing
WHERE listing_type = 'OPTION'
  AND substring(ticker FROM '^[A-Z]+') NOT IN (
      SELECT ticker
      FROM   core_banking.listing
      WHERE  listing_type = 'STOCK'
      ORDER  BY volume DESC
      LIMIT  5
  );
