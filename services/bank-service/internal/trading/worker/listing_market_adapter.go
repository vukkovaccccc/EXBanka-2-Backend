package worker

import (
	"context"
	"encoding/json"
	"time"

	"banka-backend/services/bank-service/internal/domain"
)

// listingMarketDataProvider adapts domain.ListingRepository to the
// MarketDataProvider interface needed by the trading engine.
type listingMarketDataProvider struct {
	repo domain.ListingRepository
}

// NewListingMarketDataProvider creates a MarketDataProvider backed by the
// listing repository.
func NewListingMarketDataProvider(repo domain.ListingRepository) MarketDataProvider {
	return &listingMarketDataProvider{repo: repo}
}

// GetMarketSnapshot returns Ask, Bid, Volume, and type-specific data parsed from
// details_json.  For FUTURE/OPTION: SettlementDate.  For FOREX: BaseCurrency and
// QuoteCurrency used by executeForexOrder for the currency swap.
func (p *listingMarketDataProvider) GetMarketSnapshot(ctx context.Context, listingID int64) (MarketSnapshot, error) {
	listing, err := p.repo.GetByID(ctx, listingID)
	if err != nil {
		return MarketSnapshot{}, err
	}

	snap := MarketSnapshot{
		Ask:         listing.Ask,
		Bid:         listing.Bid,
		Volume:      listing.Volume,
		ExchangeID:  listing.ExchangeID,
		ListingType: listing.ListingType,
	}

	if listing.DetailsJSON == "" {
		return snap, nil
	}

	switch listing.ListingType {
	case domain.ListingTypeForex:
		var details domain.ForexDetails
		if json.Unmarshal([]byte(listing.DetailsJSON), &details) == nil {
			snap.ForexBaseCurrency = details.BaseCurrency
			snap.ForexQuoteCurrency = details.QuoteCurrency
		}

	case domain.ListingTypeFuture, domain.ListingTypeOption:
		// Parse settlement_date. Accept both "YYYY-MM-DD" and RFC3339 formats.
		var details struct {
			SettlementDate string `json:"settlement_date"`
		}
		if json.Unmarshal([]byte(listing.DetailsJSON), &details) == nil && details.SettlementDate != "" {
			for _, layout := range []string{"2006-01-02", time.RFC3339} {
				if t, parseErr := time.Parse(layout, details.SettlementDate); parseErr == nil {
					snap.SettlementDate = &t
					break
				}
			}
		}
	}

	return snap, nil
}
