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

// GetMarketSnapshot returns Ask, Bid, Volume, and — for FUTURE/OPTION listings
// — the SettlementDate parsed from details_json.  If details_json is absent or
// does not contain settlement_date, SettlementDate is nil (STOCK/FOREX listings).
func (p *listingMarketDataProvider) GetMarketSnapshot(ctx context.Context, listingID int64) (MarketSnapshot, error) {
	listing, err := p.repo.GetByID(ctx, listingID)
	if err != nil {
		return MarketSnapshot{}, err
	}

	snap := MarketSnapshot{
		Ask:    listing.Ask,
		Bid:    listing.Bid,
		Volume: listing.Volume,
	}

	// Parse settlement_date from details_json for FUTURE and OPTION listings.
	// Accept both "YYYY-MM-DD" and RFC3339 formats (consistent with handler).
	if listing.DetailsJSON != "" {
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
