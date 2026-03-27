package oauth2

import (
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// signJWT creates a signed JWT for the service account token exchange.
func (s *serviceAccountSource) signJWT(now time.Time) (string, error) {
	header := map[string]string{
		"alg": "RS256",
		"typ": "JWT",
	}
	headerJSON, _ := json.Marshal(header)
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)

	claims := map[string]interface{}{
		"iss":   s.key.ClientEmail,
		"sub":   s.key.ClientEmail,
		"aud":   s.key.TokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
		"scope": strings.Join(s.cfg.Scopes, " "),
	}
	claimsJSON, _ := json.Marshal(claims)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)

	signingInput := headerB64 + "." + claimsB64
	hash := sha256.Sum256([]byte(signingInput))

	sig, err := s.rsaKey.Sign(rand.Reader, hash[:], crypto.SHA256)
	if err != nil {
		return "", fmt.Errorf("RSA sign: %w", err)
	}
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)

	return signingInput + "." + sigB64, nil
}
