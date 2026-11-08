package store

import (
	"database/sql"

	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func collectorColumns(cache bool) string {
	base := "id,coalesce(hostname,''),coalesce(first_seen,0),coalesce(last_seen,0),coalesce(version,''),clock_offset_ms"
	if cache {
		return base + ",cache_bytes,cache_max_bytes,cache_bytes_per_hour"
	}
	return base + ",NULL,NULL,NULL"
}

func scanCollector(row interface{ Scan(...any) error }) (CollectorRecord, error) {
	var record CollectorRecord
	var bytes, maxBytes sql.NullInt64
	var rate sql.NullFloat64
	if err := row.Scan(&record.ID, &record.Hostname, &record.FirstSeen, &record.LastSeen, &record.Version, &record.ClockOffsetMS, &bytes, &maxBytes, &rate); err != nil {
		return record, err
	}
	if maxBytes.Valid {
		var value *float64
		if rate.Valid {
			value = &rate.Float64
		}
		usage, err := pipeline.NewCacheUsage(bytes.Int64, maxBytes.Int64, value)
		if err != nil {
			return record, err
		}
		record.Cache = &usage
	}
	return record, nil
}
