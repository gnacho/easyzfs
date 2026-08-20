package totp

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestSecretFormat(t *testing.T) {
	for i := 0; i < 10; i++ {
		s, err := Secret()
		if err != nil {
			t.Fatal(err)
		}
		if len(s) != 32 { // 20 bytes → base32 sin padding = 32 chars
			t.Fatalf("secret len %d, want 32", len(s))
		}
	}
}

func TestValidateCode(t *testing.T) {
	secret, err := Secret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	code, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	if !Validate(code, secret, now) {
		t.Fatal("código válido rechazado")
	}
	if Validate("000000", secret, now) {
		t.Fatal("código inválido aceptado")
	}
}

func TestValidateSkew(t *testing.T) {
	secret, err := Secret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	// Código del slot anterior (±1) debe valer.
	prev := now.Add(-time.Second * 30)
	code, err := totp.GenerateCode(secret, prev)
	if err != nil {
		t.Fatal(err)
	}
	if !Validate(code, secret, now) {
		t.Fatal("código del slot anterior rechazado (skew ±1)")
	}
	// Slot muy antiguo (5 min) NO debe valer.
	old := now.Add(-time.Minute * 5)
	codeOld, err := totp.GenerateCode(secret, old)
	if err != nil {
		t.Fatal(err)
	}
	if Validate(codeOld, secret, now) {
		t.Fatal("código de hace 5 min aceptado")
	}
}

func TestURI(t *testing.T) {
	secret, err := Secret()
	if err != nil {
		t.Fatal(err)
	}
	uri, err := URI(secret, "admin")
	if err != nil {
		t.Fatal(err)
	}
	want := "otpauth://totp/EasyZFS:admin"
	if len(uri) < len(want) || uri[:len(want)] != want {
		t.Fatalf("uri %q no empieza por %q", uri, want)
	}
}

func TestRecoveryCodes(t *testing.T) {
	code, err := RecoveryCode()
	if err != nil {
		t.Fatal(err)
	}
	// Formato aaa-bbb-ccc-ddd.
	if len(code) != 15 || code[3] != '-' || code[7] != '-' || code[11] != '-' {
		t.Fatalf("recovery code %q con formato inesperado", code)
	}
	// Hash estable y distinto para códigos distintos.
	h1 := RecoveryHash(code)
	if RecoveryHash(code) != h1 {
		t.Fatal("hash no determinista")
	}
	c2, _ := RecoveryCode()
	if RecoveryHash(c2) == h1 {
		t.Fatal("dos códigos con el mismo hash")
	}
}

func TestRecoveryCodeAlphabet(t *testing.T) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	for i := 0; i < 50; i++ {
		code, _ := RecoveryCode()
		for _, ch := range code {
			if ch == '-' {
				continue
			}
			if !containsRune(alphabet, ch) {
				t.Fatalf("carácter %q fuera del alfabeto legible", ch)
			}
		}
	}
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}
