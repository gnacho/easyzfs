// Package totp — autenticación de dos factores TOTP (RFC 6238) para usuarios.
// Secreto por usuario en users.totp_secret; el servidor lo genera con
// pquerna/otp y valida códigos de 6 dígitos con ventana de ±1 slot.
package totp

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// Ventana de tolerancia (±1 slot de 30s) para absorber deriva de reloj.
const (
	period        = 30
	skew          = 1
	issuer        = "EasyZFS"
	recoveryCount = 10
	recoveryLen   = 10
)

// ErrBadCode — el código TOTP proporcionado no es válido.
var ErrBadCode = errors.New("código TOTP incorrecto")

// Secret codifica un secreto aleatorio en base32 sin padding (formato otpauth).
func Secret() (string, error) {
	buf := make([]byte, 20) // 160 bits
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), nil
}

// Validate comprueba un código TOTP contra un secreto (ventana ±skew slots).
func Validate(code, secret string, now time.Time) bool {
	ok, err := totp.ValidateCustom(code, secret, now, totp.ValidateOpts{
		Period:    period,
		Skew:      skew,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	return err == nil && ok
}

// URI construye la otpauth URI para el QR del autenticador.
func URI(secret, account string) (string, error) {
	secretBytes, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		return "", err
	}
	k, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: account,
		Secret:      secretBytes,
		Period:      period,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", err
	}
	return k.URL(), nil
}

// RecoveryCode genera un código de recuperación legible: 4 bloques de 3
// caracteres separados por guiones (p.ej. 'abc-def-ghi-jkl').
func RecoveryCode() (string, error) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789" // sin i,l,o,0,1 (legibles)
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	var sb strings.Builder
	for i, b := range buf {
		sb.WriteByte(alphabet[int(b)%len(alphabet)])
		if i%3 == 2 && i != 11 {
			sb.WriteByte('-')
		}
	}
	return sb.String(), nil
}

// RecoveryHash devuelve el hash SHA-256 en hex de un código de recuperación
// (se almacena hasheado; nunca en claro, como las contraseñas).
func RecoveryHash(code string) string {
	sum := sha256.Sum256([]byte(code))
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:])
}
