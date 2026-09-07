package email

import "context"

// attachEmailDates 批量补来源邮件收到时间（Unix 秒）。
func (s *Store) attachEmailDates(ctx context.Context, invoices []Invoice) error {
	if len(invoices) == 0 {
		return nil
	}
	ids := make([]string, 0, len(invoices))
	seen := map[string]struct{}{}
	for _, inv := range invoices {
		if inv.EmailID == "" {
			continue
		}
		if _, ok := seen[inv.EmailID]; ok {
			continue
		}
		seen[inv.EmailID] = struct{}{}
		ids = append(ids, inv.EmailID)
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id, date FROM emails WHERE id = ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	dates := map[string]int64{}
	for rows.Next() {
		var id string
		var date int64
		if err := rows.Scan(&id, &date); err != nil {
			return err
		}
		dates[id] = date
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range invoices {
		invoices[i].EmailDate = dates[invoices[i].EmailID]
	}
	return nil
}
