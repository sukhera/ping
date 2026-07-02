package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func testRSAKeys(t *testing.T) (*rsa.PrivateKey, *rsa.PublicKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return priv, &priv.PublicKey
}

func TestIssueAndParseAccessToken_RoundTrip(t *testing.T) {
	priv, pub := testRSAKeys(t)

	token, err := issueAccessToken(priv, "user-123", 15*time.Minute)
	if err != nil {
		t.Fatalf("issueAccessToken: %v", err)
	}

	claims, err := parseAccessToken(pub, token)
	if err != nil {
		t.Fatalf("parseAccessToken: %v", err)
	}
	if claims.Subject != "user-123" {
		t.Errorf("Subject = %q, want user-123", claims.Subject)
	}
	if claims.Issuer != jwtIssuer {
		t.Errorf("Issuer = %q, want %q", claims.Issuer, jwtIssuer)
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != jwtAudience {
		t.Errorf("Audience = %v, want [%q]", claims.Audience, jwtAudience)
	}
	if claims.ID == "" {
		t.Error("expected non-empty jti")
	}
}

func TestParseAccessToken_Expired(t *testing.T) {
	priv, pub := testRSAKeys(t)

	token, err := issueAccessToken(priv, "user-123", -1*time.Minute)
	if err != nil {
		t.Fatalf("issueAccessToken: %v", err)
	}

	if _, err := parseAccessToken(pub, token); err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
}

func TestParseAccessToken_WrongAudience(t *testing.T) {
	priv, pub := testRSAKeys(t)

	claims := accessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-123",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			Issuer:    jwtIssuer,
			Audience:  jwt.ClaimStrings{"someone-else"},
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(priv)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	if _, err := parseAccessToken(pub, signed); err == nil {
		t.Fatal("expected error for wrong audience, got nil")
	}
}

func TestParseAccessToken_WrongIssuer(t *testing.T) {
	priv, pub := testRSAKeys(t)

	claims := accessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-123",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			Issuer:    "not-ping",
			Audience:  jwt.ClaimStrings{jwtAudience},
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(priv)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	if _, err := parseAccessToken(pub, signed); err == nil {
		t.Fatal("expected error for wrong issuer, got nil")
	}
}

func TestParseAccessToken_TamperedSignature(t *testing.T) {
	priv, pub := testRSAKeys(t)

	token, err := issueAccessToken(priv, "user-123", 15*time.Minute)
	if err != nil {
		t.Fatalf("issueAccessToken: %v", err)
	}

	// Flip a character in the middle of the signature segment (not the last
	// char, which can be base64 padding that round-trips to the same bytes).
	parts := strings.Split(token, ".")
	sig := []byte(parts[2])
	mid := len(sig) / 2
	if sig[mid] == 'a' {
		sig[mid] = 'b'
	} else {
		sig[mid] = 'a'
	}
	tampered := parts[0] + "." + parts[1] + "." + string(sig)

	if _, err := parseAccessToken(pub, tampered); err == nil {
		t.Fatal("expected error for tampered signature, got nil")
	}
}

// TestParseAccessToken_RejectsAlgConfusion guards against the classic JWT
// "alg confusion" attack: a token that claims HS256 and is "signed" using
// the RSA public key's bytes as an HMAC secret must be rejected, because
// parseAccessToken restricts accepted algorithms to RS256 only.
func TestParseAccessToken_RejectsAlgConfusion(t *testing.T) {
	_, pub := testRSAKeys(t)

	claims := accessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-123",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			Issuer:    jwtIssuer,
			Audience:  jwt.ClaimStrings{jwtAudience},
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	signed, err := token.SignedString(pubDER)
	if err != nil {
		t.Fatalf("sign HS256 token: %v", err)
	}

	if _, err := parseAccessToken(pub, signed); err == nil {
		t.Fatal("expected error rejecting HS256-signed token, got nil")
	}
}
