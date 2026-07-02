package server

import (
	"crypto/rsa"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	jwtIssuer   = "ping"
	jwtAudience = "ping-api"
)

type accessClaims struct {
	jwt.RegisteredClaims
}

// issueAccessToken signs a short-lived RS256 access token. Claims are kept
// minimal (sub, jti, iat, exp, iss, aud) since the JWT payload is base64,
// not encrypted.
func issueAccessToken(priv *rsa.PrivateKey, userID string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := accessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			ID:        newRequestID(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			Issuer:    jwtIssuer,
			Audience:  jwt.ClaimStrings{jwtAudience},
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(priv)
	if err != nil {
		return "", fmt.Errorf("server: sign access token: %w", err)
	}
	return signed, nil
}

// parseAccessToken validates signature, algorithm, issuer, audience, and
// expiry. Restricting valid methods to RS256 blocks the classic JWT "alg
// confusion" attack (a token claiming alg:none or HMAC using the public key
// as the HMAC secret).
func parseAccessToken(pub *rsa.PublicKey, tokenString string) (*accessClaims, error) {
	claims := &accessClaims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		return pub, nil
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(jwtIssuer), jwt.WithAudience(jwtAudience))
	if err != nil {
		return nil, fmt.Errorf("server: parse access token: %w", err)
	}
	return claims, nil
}
