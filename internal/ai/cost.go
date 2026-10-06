package ai

// CalculateCost fills u.Cost from the model's rates.
func CalculateCost(m Model, u *Usage) {
	const perMillion = 1_000_000
	r := m.Cost
	u.Cost.Input = r.Input / perMillion * float64(u.Input)
	u.Cost.Output = r.Output / perMillion * float64(u.Output)
	u.Cost.CacheRead = r.CacheRead / perMillion * float64(u.CacheRead)
	u.Cost.CacheWrite = r.CacheWrite / perMillion * float64(u.CacheWrite)
	u.Cost.Total = u.Cost.Input + u.Cost.Output + u.Cost.CacheRead + u.Cost.CacheWrite
}

// ContextTokens is how many tokens of context a request used: the total the
// provider reported, or the sum of the parts when it reported none.
func ContextTokens(u Usage) int {
	if u.TotalTokens > 0 {
		return u.TotalTokens
	}
	return u.Input + u.Output + u.CacheRead + u.CacheWrite
}
