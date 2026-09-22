package storage

import "context"

// ProviderBudget keeps the fallback's budget separate from the existing Gemini
// counter. The original api_usage table and its history remain unchanged.
type ProviderBudget struct {
	Store    *Store
	Provider string
}

func (b ProviderBudget) AllowAPI(ctx context.Context, date string, limit int) (bool, error) {
	if limit <= 0 {
		return false, nil
	}
	r, err := b.Store.db.ExecContext(ctx, `INSERT INTO provider_api_usage(provider,utc_date,requests) VALUES(?,?,1)
 ON CONFLICT(provider,utc_date) DO UPDATE SET requests=requests+1 WHERE requests<?`, b.Provider, date, limit)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}
