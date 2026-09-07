package usersetting

// Decision is the LWW outcome when a client PUT meets a stored row.
type Decision int

const (
	// DecisionApply writes the client row (missing server row, or client newer).
	DecisionApply Decision = iota
	// DecisionKeep keeps the server row (server newer or equal).
	DecisionKeep
)

// DecidePut compares unix-second stamps. The later stamp wins.
// Equal stamps keep the server row so a replay does not churn updated_at.
func DecidePut(clientUpdated, serverUpdated int64) Decision {
	if clientUpdated > serverUpdated {
		return DecisionApply
	}
	return DecisionKeep
}
