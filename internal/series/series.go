// Package series — consulta y downsampling de la tabla series (U2).
// La tabla guarda muestras crudas (source, ts RFC3339, value) con retención
// configurable (RETENTION_DAYS, def 30). El endpoint de rangos aplica LTTB
// server-side para no ahogar al navegador (patrón sqlite-timeseries-daemon).
package series

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Point — punto de una serie (ts epoch UTC segundos, value).
type Point struct {
	Ts    int64   `json:"ts"`
	Value float64 `json:"value"`
}

// Range consulta una fuente entre from y to (epoch segundos) y devuelve hasta
// threshold puntos muestreados con LTTB. Rango vacío → slice vacío (200).
// Usa series_daily (agregados diarios) para la parte del rango más vieja que
// rawRetentionDays; combina ambas consultas y ordena por ts.
func Range(ctx context.Context, db *sql.DB, source string, from, to int64, threshold int, rawRetentionDays int) ([]Point, error) {
	if to <= from {
		return []Point{}, nil
	}
	cutoff := time.Now().UTC().Add(-time.Duration(rawRetentionDays) * 24 * time.Hour).Unix()
	raw := []Point{}
	if to > cutoff {
		rawStart := from
		if rawStart < cutoff {
			rawStart = cutoff
		}
		var err error
		raw, err = rangeTable(ctx, db, source, rawStart, to)
		if err != nil {
			return nil, err
		}
	}
	daily := []Point{}
	if from < cutoff {
		dailyEnd := to
		if dailyEnd > cutoff {
			dailyEnd = cutoff
		}
		var err error
		daily, err = rangeDaily(ctx, db, source, from, dailyEnd)
		if err != nil {
			return nil, err
		}
	}
	merged := mergePoints(daily, raw)
	if len(merged) <= threshold {
		return merged, nil
	}
	return LTTB(merged, threshold), nil
}

// rangeTable consulta las muestras crudas de series.
func rangeTable(ctx context.Context, db *sql.DB, source string, from, to int64) ([]Point, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT CAST(strftime('%s', ts) AS INTEGER), value FROM series
		 WHERE source = ? AND ts >= datetime(?, 'unixepoch') AND ts < datetime(?, 'unixepoch')
		 ORDER BY ts`, source, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Point{}
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.Ts, &p.Value); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// rangeDaily consulta los agregados diarios (series_daily). El punto diario se
// sitúa a mediodía UTC del día (timestamp representativo de la media).
func rangeDaily(ctx context.Context, db *sql.DB, source string, from, to int64) ([]Point, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT CAST(strftime('%s', day || 'T12:00:00Z') AS INTEGER), avg FROM series_daily
		 WHERE source = ? AND day >= date(?, 'unixepoch') AND day < date(?, 'unixepoch')
		 ORDER BY day`, source, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Point{}
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.Ts, &p.Value); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// mergePoints fusiona dos series ya ordenadas por ts (daily primero, luego
// raw), evitando duplicados en la frontera del cutoff.
func mergePoints(a, b []Point) []Point {
	out := make([]Point, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if a[i].Ts <= b[j].Ts {
			out = append(out, a[i])
			i++
		} else {
			out = append(out, b[j])
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}

// LTTB — Largest-Triangle-Three-Buckets: conserva la forma visual (picos y
// valles) reduciendo la serie a `threshold` puntos. Adaptado del asset Go de
// la skill sqlite-timeseries-daemon.
func LTTB(data []Point, threshold int) []Point {
	n := len(data)
	if threshold >= n || threshold <= 0 {
		return data
	}
	out := make([]Point, 0, threshold)
	out = append(out, data[0])
	every := float64(n-2) / float64(threshold-2)
	a := 0
	for i := 0; i < threshold-2; i++ {
		rangeStart := int(float64(i+1)*every) + 1
		rangeEnd := int(float64(i+2)*every) + 1
		if rangeEnd > n {
			rangeEnd = n
		}
		var avgX, avgY float64
		for j := rangeStart; j < rangeEnd; j++ {
			avgX += float64(data[j].Ts)
			avgY += data[j].Value
		}
		avgX /= float64(rangeEnd - rangeStart)
		avgY /= float64(rangeEnd - rangeStart)
		bucketStart := int(float64(i)*every) + 1
		bucketEnd := int(float64(i+1)*every) + 1
		ax, ay := float64(data[a].Ts), data[a].Value
		maxArea := -1.0
		nextA := bucketStart
		for j := bucketStart; j < bucketEnd; j++ {
			area := abs((ax-avgX)*(data[j].Value-ay) - (ax-float64(data[j].Ts))*(avgY-ay))
			if area > maxArea {
				maxArea = area
				nextA = j
			}
		}
		out = append(out, data[nextA])
		a = nextA
	}
	return append(out, data[n-1])
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// Sources válidas para el endpoint: pool.<name>.used_pct y disk.<dev>.temp.
// El prefix protege de inyección/patrones raros (whitelist por prefijo).
func ValidSource(src string) bool {
	switch {
	case len(src) > 5 && src[:5] == "pool." && len(src) > 6 && src[5] != '.':
		return true
	case len(src) > 5 && src[:5] == "disk." && len(src) > 6 && src[5] != '.':
		return true
	}
	return false
}

// ParseDays convierte el parámetro ?days= a rango epoch. Acepta 1-1825
// (5 años de tendencia diaria; la retención larga la garantiza series_daily).
func ParseDays(days int) (from, to int64, err error) {
	if days < 1 || days > 1825 {
		return 0, 0, fmt.Errorf("days fuera de rango (1-1825): %d", days)
	}
	now := time.Now().UTC()
	to = now.Unix()
	from = now.Add(-time.Duration(days) * 24 * time.Hour).Unix()
	return from, to, nil
}
