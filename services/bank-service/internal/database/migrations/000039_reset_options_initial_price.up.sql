-- =============================================================================
-- Migration: 000039_reset_options_initial_price
-- Service:   bank-service
-- Schema:    core_banking
--
-- Briše sve OPTION listinge i njihove dnevne zapise kako bi worker pri
-- sledećem pokretanju regenerisao opcije sa ispravnim initial_price poljem
-- u details_json. To polje je neophodno za smisleno računanje promene %.
-- =============================================================================

DELETE FROM core_banking.listing_daily_price_info
WHERE listing_id IN (
    SELECT id FROM core_banking.listing WHERE listing_type = 'OPTION'
);

DELETE FROM core_banking.listing WHERE listing_type = 'OPTION';
