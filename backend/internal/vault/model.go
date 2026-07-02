package vault

// Version is a retained vault blob version, for surfacing sync conflicts.
type Version struct {
	Version   int    `json:"version"`
	IsCurrent bool   `json:"isCurrent"`
	UpdatedAt int64  `json:"updatedAt"`
}
