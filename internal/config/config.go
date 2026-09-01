// Package config — env → struct validada. Fallo aquí = exit 1 con mensaje claro.
package config

import (
	"crypto/rand"
	"crypto/sha256"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config — configuración de la app (todo por variables de entorno).
type Config struct {
	ListenAddr    string // LISTEN_ADDR (def ":8080")
	DBPath        string // DB_PATH (def "/var/lib/easyzfs/app.db")
	SessionSecret []byte // SESSION_SECRET (sha256 del valor; si falta, efímero + aviso)
	Demo          bool   // DEMO=1 → datos mock + mutaciones 403 demo_mode
	Mock          bool   // MOCK=1 → colectores mock (datos reales mutan y fallarán)
	AdminPassword string // ADMIN_PASSWORD para bootstrap del primer admin
	CookieSecure  bool   // COOKIE_SECURE=1 → atributo Secure (tras proxy TLS)
	RetentionDays int    // RETENTION_DAYS series (def 30)

	VAPIDPublicKey  string // VAPID_PUBLIC_KEY (Web Push; la genera el instalador)
	VAPIDPrivateKey string // VAPID_PRIVATE_KEY (solo servidor; si falta, push desactivado)
	VAPIDSubject    string // VAPID_SUBJECT (def "mailto:easyzfs@localhost"; siempre mailto:)

	WebhookSecret  string        // WEBHOOK_SECRET (firma HMAC del webhook saliente)
	WebhookTimeout time.Duration // WEBHOOK_TIMEOUT en segundos (def 10)
	WebhookRetries int           // WEBHOOK_RETRIES (def 3)

	// Email/SMTP (canal de alertas S5). Vacio = email desactivado.
	SMTPHost       string        // SMTP_HOST
	SMTPPort       int           // SMTP_PORT (def 587)
	SMTPUser       string        // SMTP_USER (vacío = SMTP sin autenticación)
	SMTPPass       string        // SMTP_PASS (solo servidor; nunca en logs ni BD)
	SMTPFrom       string        // SMTP_FROM ("EasyZFS <easyzfs@example.com>")
	SMTPEncryption string        // SMTP_ENCRYPTION: none | starttls | tls (def starttls)
	SMTPTimeout    time.Duration // SMTP_TIMEOUT (def 10s)
	SMTPTestTo     string        // SMTP_TEST_TO: fuerza destino de prueba en todos los envíos

	// Canales de alerta adicionales (#86). Vacio = canal desactivado.
	NtfyURL        string // NTFY_URL (p.ej. https://ntfy.sh/mi-topic)
	NtfyToken      string // NTFY_TOKEN (opcional; vacío = sin auth)
	GotifyURL      string // GOTIFY_URL (p.ej. https://gotify.example.com)
	GotifyToken    string // GOTIFY_TOKEN (app token de Gotify)
	SyslogHost     string // SYSLOG_HOST (p.ej. 127.0.0.1)
	SyslogPort     int    // SYSLOG_PORT (def 514)
	SyslogProto    string // SYSLOG_PROTO: udp | tcp (def udp)
	SyslogFacility int    // SYSLOG_FACILITY (def 1 = user)

	// Intervalo del colector principal de ZFS (#124). Valores altos reducen
	// el numero de comandos sudo y el volumen de logs de auditoria.
	ZpoolInterval time.Duration // EASYZFS_ZPOOL_INTERVAL en segundos (def 60)
}

// DataDir — directorio de datos del daemon (deriva de DB_PATH): ahí viven la
// BD, los backups y el material SSH de la replicación (ssh/id_ed25519 y
// ssh/known_hosts — nunca el ~/.ssh del sistema).
func (c *Config) DataDir() string {
	return filepath.Dir(c.DBPath)
}

// PushEnabled — Web Push operativo: hacen falta AMBAS claves VAPID.
// Sin ellas el servidor arranca igual (aviso en log) y los endpoints de push
// devuelven 503 push_not_configured (el instalador las autoconfigura).
func (c *Config) PushEnabled() bool {
	return c.VAPIDPublicKey != "" && c.VAPIDPrivateKey != ""
}

// Load lee y valida la configuración. No falla: valores por defecto sensatos + avisos.
func Load() *Config {
	cfg := &Config{
		ListenAddr:    env("LISTEN_ADDR", ":8080"),
		DBPath:        env("DB_PATH", "/var/lib/easyzfs/app.db"),
		Demo:          envBool("DEMO"),
		Mock:          envBool("MOCK"),
		AdminPassword: os.Getenv("ADMIN_PASSWORD"),
		CookieSecure:  envBool("COOKIE_SECURE"),
		RetentionDays: envInt("RETENTION_DAYS", 30),

		VAPIDPublicKey:  os.Getenv("VAPID_PUBLIC_KEY"),
		VAPIDPrivateKey: os.Getenv("VAPID_PRIVATE_KEY"),
		VAPIDSubject:    env("VAPID_SUBJECT", "mailto:easyzfs@localhost"),

		WebhookSecret:  os.Getenv("WEBHOOK_SECRET"),
		WebhookTimeout: time.Duration(envInt("WEBHOOK_TIMEOUT", 10)) * time.Second,
		WebhookRetries: envInt("WEBHOOK_RETRIES", 3),

		SMTPHost:       os.Getenv("SMTP_HOST"),
		SMTPPort:       envInt("SMTP_PORT", 587),
		SMTPUser:       os.Getenv("SMTP_USER"),
		SMTPPass:       os.Getenv("SMTP_PASS"),
		SMTPFrom:       os.Getenv("SMTP_FROM"),
		SMTPEncryption: env("SMTP_ENCRYPTION", "starttls"),
		SMTPTimeout:    time.Duration(envInt("SMTP_TIMEOUT", 10)) * time.Second,
		SMTPTestTo:     os.Getenv("SMTP_TEST_TO"),

		NtfyURL:        os.Getenv("NTFY_URL"),
		NtfyToken:      os.Getenv("NTFY_TOKEN"),
		GotifyURL:      os.Getenv("GOTIFY_URL"),
		GotifyToken:    os.Getenv("GOTIFY_TOKEN"),
		SyslogHost:     os.Getenv("SYSLOG_HOST"),
		SyslogPort:     envInt("SYSLOG_PORT", 514),
		SyslogProto:    env("SYSLOG_PROTO", "udp"),
		SyslogFacility: envInt("SYSLOG_FACILITY", 1),

		ZpoolInterval: time.Duration(envInt("EASYZFS_ZPOOL_INTERVAL", 60)) * time.Second,
	}
	if cfg.Demo {
		cfg.Mock = true // demo implica colectores mock
	}
	// Placeholder detection (30-Ago-2026, leccion Yuvomi)
	placeholderMarkers := []string{"cambia", "changeme", "change-me", "example", "placeholder", "your-secret", "replace_me", "xxx"}
	for _, kv := range []struct{ key, val string }{
		{"ADMIN_PASSWORD", cfg.AdminPassword},
		{"WEBHOOK_SECRET", cfg.WebhookSecret},
		{"SMTP_PASS", cfg.SMTPPass},
	} {
		low := strings.ToLower(kv.val)
		for _, m := range placeholderMarkers {
			if kv.val != "" && strings.Contains(low, m) {
				log.Fatalf("config: %s contiene un valor de ejemplo del .env.example; genera un secreto real", kv.key)
			}
		}
	}

	secret := os.Getenv("SESSION_SECRET")
	if secret == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			log.Fatalf("no se pudo generar SESSION_SECRET efímero: %v", err)
		}
		cfg.SessionSecret = b
		log.Println("aviso: SESSION_SECRET no definido; usando secreto efímero (las sesiones no sobreviven a reinicios)")
	} else {
		sum := sha256.Sum256([]byte(secret))
		cfg.SessionSecret = sum[:]
	}
	// Push es opcional: sin clave privada NO se sale; se desactiva con aviso
	// (deploy/install.sh la autoconfigura con `-generate-vapid`).
	if cfg.VAPIDPrivateKey == "" {
		if cfg.VAPIDPublicKey != "" {
			log.Println("aviso: VAPID_PUBLIC_KEY sin VAPID_PRIVATE_KEY; notificaciones push desactivadas")
		} else {
			log.Println("aviso: claves VAPID no configuradas; notificaciones push desactivadas (deploy/install.sh las genera)")
		}
	}
	// Email es opcional: sin SMTP_HOST no se sale; se desactiva con aviso.
	if cfg.SMTPHost == "" {
		log.Println("aviso: SMTP_HOST no configurado; notificaciones por email desactivadas")
	} else if cfg.SMTPFrom == "" {
		log.Println("aviso: SMTP_FROM no configurado; notificaciones por email desactivadas")
	}
	return cfg
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string) bool {
	v := os.Getenv(key)
	return v == "1" || v == "true" || v == "yes"
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
