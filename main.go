// main.go — entrypoint de EasyZFS.
// Wiring: config → db (migraciones) → settings → usuarios (bootstrap) →
// hub SSE → alerter → colectores → acciones → scheduler → HTTP.
// Graceful shutdown: drenar SSE → srv.Shutdown → cerrar SQLite.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"easyzfs/internal/actions"
	"easyzfs/internal/alerts"
	"easyzfs/internal/auth"
	"easyzfs/internal/backup"
	"easyzfs/internal/collectors"
	"easyzfs/internal/config"
	"easyzfs/internal/db"
	"easyzfs/internal/httpapi"
	"easyzfs/internal/hub"
	"easyzfs/internal/longops"
	"easyzfs/internal/push"
	"easyzfs/internal/replication"
	"easyzfs/internal/scheduler"
	"easyzfs/internal/security"
	"easyzfs/internal/settings"
	"easyzfs/internal/updater"
	"easyzfs/internal/users"
	"easyzfs/internal/webhook"
)

// Inyectadas por ldflags (-X main.version=... -X main.build=...).
var (
	version = "dev"
	build   = ""
)

//go:embed dist
var distFS embed.FS

func main() {
	// -generate-vapid: imprime un par de claves VAPID para /etc/easyzfs/env
	// (lo usa deploy/install.sh) y sale 0. No toca BD ni configuración.
	genVapid := flag.Bool("generate-vapid", false, "genera un par de claves VAPID (Web Push) y sale")
	flag.Parse()
	if *genVapid {
		priv, pub, err := webpush.GenerateVAPIDKeys()
		if err != nil {
			log.Fatalf("generate-vapid: %v", err)
		}
		fmt.Printf("VAPID_PUBLIC_KEY=%s\nVAPID_PRIVATE_KEY=%s\n", pub, priv)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.Load()

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("sqlite: %v", err)
	}
	if err := db.Migrate(ctx, database); err != nil {
		log.Fatalf("migraciones: %v", err)
	}

	stStore, err := settings.NewStore(database)
	if err != nil {
		log.Fatalf("settings: %v", err)
	}

	userStore := users.NewStore(database)
	if err := userStore.Bootstrap(ctx, cfg.AdminPassword); err != nil {
		log.Fatalf("bootstrap usuarios: %v", err)
	}

	h := hub.NewHub()
	alerter := alerts.New(database, h, stStore)

	// Webhook saliente (issue #18): worker async con cola acotada + DLQ. La URL
	// se resuelve de settings en cada envío (dinámica, editable por UI); secret,
	// timeout y retries vienen de env leídos una vez al arranque (bootstrap).
	webhookNotifier := webhook.NewNotifier(webhook.Config{
		Secret:     cfg.WebhookSecret,
		Timeout:    cfg.WebhookTimeout,
		Retries:    cfg.WebhookRetries,
		RetryDelay: time.Second,
	}, database, func() string {
		st, err := stStore.Load(context.Background())
		if err != nil {
			return ""
		}
		return st.Webhook
	})
	alerter.SetWebhook(webhookNotifier)

	// Sender Web Push: inerte si faltan claves VAPID; en demo nunca envía.
	pushSender := push.New(cfg, database, h)
	alerter.SetPush(pushSender)
	// Ticker de la cola de quiet hours (60 s): entrega diferida al terminar
	// la ventana de silencio. En demo o sin VAPID queda inerte.
	go pushSender.RunQueue(ctx)

	// Colectores (reales o mock) + providers para los handlers.
	providers, cols := collectors.Build(cfg, database, h, alerter)

	act := actions.NewService(database)
	jobStore := scheduler.NewStore(database)
	sched := scheduler.New(jobStore, act, h, providers.Disks.Disks)

	// Replicación ZFS send/recv (lote C): store propio + ejecución vía longops.
	longOps := longops.New(h)
	replRunner := replication.NewRunner(replication.NewStore(database), longOps, h, jobStore, cfg.DataDir(), cfg.Mock)
	go replRunner.Run(ctx)

	// Copia de seguridad de la BD: colector por frecuencia horaria + handlers.
	backupStore := backup.New(database, cfg.DBPath, stStore)
	go backupStore.RunLoop(ctx)

	// Updater: detecta releases semver y prepara el apply (el swap lo hace la
	// unit easyzfs-update.path). Inerte si version=dev o sin DATA_DIR escribible.
	updaterSvc := updater.New(version, cfg.DataDir())

	// Versión de OpenZFS del host (una vez, al arranque).
	zfsVersion := "mock"
	if !cfg.Mock {
		detCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		zfsVersion = collectors.DetectZFSVersion(detCtx)
		cancel()
	}

	srv := httpapi.NewServer(httpapi.Deps{
		Cfg: cfg, DB: database, Auth: auth.NewManager(database, cfg.SessionSecret, cfg.CookieSecure),
		Users: userStore, Alerter: alerter, Settings: stStore,
		Pools: providers.Pools, Disks: providers.Disks, SysTimers: providers.SysTimers,
		Perf: providers.Perf, Caps: providers.Caps,
		Actions: act, Sched: sched, Jobs: jobStore, Hub: h, Push: pushSender,
		Backup: backupStore, LongOps: longOps, Repl: replRunner, Updater: updaterSvc,
		Version: version, Build: build, ZFSVersion: zfsVersion,
	})

	mux := http.NewServeMux()
	mux.Handle("/api/", srv.Handler())

	// SPA embebida: estáticos + fallback a index.html.
	webFS, err := fs.Sub(distFS, "dist")
	if err != nil {
		log.Fatalf("embed dist: %v", err)
	}
	mux.Handle("/", spaHandler(http.FS(webFS)))

	httpSrv := &http.Server{
		Addr: cfg.ListenAddr,
		// Cabeceras de seguridad HTTP en TODAS las respuestas (auditoría P3).
		Handler:           security.Middleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
		// Sin WriteTimeout global: mataría las conexiones SSE.
	}

	// Arranque de colectores y scheduler (goroutines con ctx cancelable).
	for _, c := range cols {
		go c.Run(ctx)
	}
	go sched.Run(ctx)

	go func() {
		log.Printf("EasyZFS %s escuchando en %s (mock=%v demo=%v)", version, cfg.ListenAddr, cfg.Mock, cfg.Demo)
		if err := httpSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("apagando: drenando conexiones SSE y HTTP…")
	h.Close() // cierra clientes SSE con evento 'bye'
	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	// Para el worker del webhook ANTES de cerrar SQLite (la DLQ escribe en BD).
	webhookNotifier.Close()
	if err := database.Close(); err != nil {
		log.Printf("sqlite close: %v", err)
	}
	log.Println("apagado limpio")
}

// spaHandler sirve la SPA embebida: fichero si existe, index.html si no
// (fallback de rutas del cliente). Cache-Control: index.html y sw.js siempre
// se revalidan (no-cache) para que un despliegue nuevo se vea al recargar;
// /assets/* es inmutable (Vite les pone hash en el nombre — si el hash cambia,
// es un fichero distinto); el resto (iconos, manifest) con caché corta.
func spaHandler(fsys http.FileSystem) http.Handler {
	fileSrv := http.FileServer(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/" || path == "/index.html" || path == "/sw.js":
			w.Header().Set("Cache-Control", "no-cache")
		case strings.HasPrefix(path, "/assets/"):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		default:
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		if path == "/" {
			path = "/index.html"
		}
		f, err := fsys.Open(path)
		if err != nil {
			// fallback SPA: cualquier ruta no encontrada devuelve index.html
			w.Header().Set("Cache-Control", "no-cache")
			r.URL.Path = "/"
			fileSrv.ServeHTTP(w, r)
			return
		}
		f.Close()
		fileSrv.ServeHTTP(w, r)
	})
}
