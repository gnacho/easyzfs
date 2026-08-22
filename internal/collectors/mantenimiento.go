// mantenimiento.go — purga diaria (03:30): series fuera de retención, sesiones
// expiradas, alertas/historial antiguos; checkpoint WAL semanal (domingos).
package collectors

import (
	"context"
	"database/sql"
	"log"
	"time"

	"easyzfs/internal/db"
)

const (
	alertsRetentionDays      = 90
	historyRetentionDays     = 180
	seriesDailyRetentionDays = 1825 // 5 años de tendencia diaria (#85)
)

// Mantenimiento — colector diario de limpieza (lección 4: toda serie tiene retención).
type Mantenimiento struct {
	db            *sql.DB
	retentionDays int
	lastRun       string // 'YYYY-MM-DD' de la última ejecución
}

// NewMantenimiento crea el colector de mantenimiento.
func NewMantenimiento(d *sql.DB, retentionDays int) *Mantenimiento {
	return &Mantenimiento{db: d, retentionDays: retentionDays}
}

// Name implementa Collector.
func (m *Mantenimiento) Name() string { return "mantenimiento" }

// Run — comprueba cada minuto si toca la purga diaria (03:30 hora local).
func (m *Mantenimiento) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	m.maybeRun(ctx) // por si el arranque cae en la ventana
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.maybeRun(ctx)
		}
	}
}

// maybeRun ejecuta la purga una vez al día a partir de las 03:30.
func (m *Mantenimiento) maybeRun(ctx context.Context) {
	now := time.Now()
	today := now.Format("2006-01-02")
	if now.Hour() < 3 || (now.Hour() == 3 && now.Minute() < 30) || m.lastRun == today {
		return
	}
	m.lastRun = today
	m.purge(ctx)
}

// purge — la limpieza propiamente dicha. Fallos: log y seguir (no críticos).
func (m *Mantenimiento) purge(ctx context.Context) {
	// 1. Agregar crudas viejas a series_daily ANTES de purgarlas (tendencia larga).
	if n, err := db.RollupSeriesToDaily(ctx, m.db, m.retentionDays); err != nil {
		log.Printf("mantenimiento: rollup series→daily: %v", err)
	} else if n > 0 {
		log.Printf("mantenimiento: %d puntos de serie agregados a diario", n)
	}
	if n, err := db.PurgeSeries(ctx, m.db, m.retentionDays); err != nil {
		log.Printf("mantenimiento: purga series: %v", err)
	} else if n > 0 {
		log.Printf("mantenimiento: %d puntos de serie purgados", n)
	}
	if n, err := db.PurgeSeriesDaily(ctx, m.db, seriesDailyRetentionDays); err != nil {
		log.Printf("mantenimiento: purga series_daily: %v", err)
	} else if n > 0 {
		log.Printf("mantenimiento: %d días de serie diaria purgados", n)
	}
	if err := db.PurgeSessions(ctx, m.db); err != nil {
		log.Printf("mantenimiento: purga sesiones: %v", err)
	}
	if err := db.PurgeAlerts(ctx, m.db, alertsRetentionDays); err != nil {
		log.Printf("mantenimiento: purga alertas: %v", err)
	}
	if err := db.PurgeJobHistory(ctx, m.db, historyRetentionDays); err != nil {
		log.Printf("mantenimiento: purga historial jobs: %v", err)
	}
	if time.Now().Weekday() == time.Sunday {
		if err := db.Checkpoint(ctx, m.db); err != nil {
			log.Printf("mantenimiento: checkpoint WAL: %v", err)
		}
	}
}
