package middleware

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const UserIDKey contextKey = "userID"

// --- JWKS Caching Logic ---

var (
	jwksCache     = make(map[string]*ecdsa.PublicKey)
	jwksCacheMux  sync.RWMutex
	lastJWKSFetch time.Time
)

type JWKS struct {
	Keys []JWK `json:"keys"`
}

type JWK struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func getSupabasePublicKey(kid string) (*ecdsa.PublicKey, error) {
	// 1. Check Cache (Read Lock)
	jwksCacheMux.RLock()
	key, exists := jwksCache[kid]
	jwksCacheMux.RUnlock()
	if exists {
		return key, nil
	}

	// 2. Fetch from Supabase (Write Lock)
	jwksCacheMux.Lock()
	defer jwksCacheMux.Unlock()

	// Double-check cache in case another goroutine just updated it
	if key, exists := jwksCache[kid]; exists {
		return key, nil
	}

	// Rate limit: Don't fetch more than once every 10 seconds
	if time.Since(lastJWKSFetch) < 10*time.Second {
		log.Printf("DEBUG: Rate limit active. Key %s not found in cache.", kid)
		return nil, fmt.Errorf("key %s not found (rate limit active)", kid)
	}

	supabaseURL := os.Getenv("SUPABASE_URL")
	if supabaseURL == "" {
		log.Println("ERROR: SUPABASE_URL environment variable is not set")
		return nil, fmt.Errorf("SUPABASE_URL environment variable is not set")
	}

	log.Printf("DEBUG: Fetching JWKS from %s/auth/v1/.well-known/jwks.json", supabaseURL)
	resp, err := http.Get(supabaseURL + "/auth/v1/.well-known/jwks.json")
	if err != nil {
		log.Printf("ERROR: Failed to fetch JWKS: %v", err)
		return nil, fmt.Errorf("failed to fetch JWKS: %v", err)
	}
	defer resp.Body.Close()

	var jwks JWKS
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		log.Printf("ERROR: Failed to decode JWKS JSON: %v", err)
		return nil, fmt.Errorf("failed to decode JWKS: %v", err)
	}

	lastJWKSFetch = time.Now()
	log.Printf("DEBUG: Fetched %d keys from Supabase", len(jwks.Keys))

	// Parse and cache keys
	for _, k := range jwks.Keys {
		if k.Kty == "EC" && k.Crv == "P-256" {
			xBytes, _ := base64.RawURLEncoding.DecodeString(k.X)
			yBytes, _ := base64.RawURLEncoding.DecodeString(k.Y)

			if len(xBytes) > 0 && len(yBytes) > 0 {
				jwksCache[k.Kid] = &ecdsa.PublicKey{
					Curve: elliptic.P256(),
					X:     new(big.Int).SetBytes(xBytes),
					Y:     new(big.Int).SetBytes(yBytes),
				}
			}
		}
	}

	if key, exists := jwksCache[kid]; exists {
		return key, nil
	}

	log.Printf("ERROR: Key ID %s not found in Supabase JWKS", kid)
	return nil, fmt.Errorf("key id %s not found in Supabase JWKS", kid)
}

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 7. A user tries to connect. The middleware intercepts the request and looks for the JWT token.
		// For WebSockets, tokens are often passed in the query string
		// because the browser's WebSocket API doesn't support custom headers.
		tokenString := r.URL.Query().Get("token")

		// Fallback to Header if you're testing via Postman/CURL
		if tokenString == "" {
			authHeader := r.Header.Get("Authorization")
			tokenString = strings.TrimPrefix(authHeader, "Bearer ")
		}

		if tokenString == "" {
			log.Println("DEBUG: No token provided in request")
			http.Error(w, "Unauthorized: No token provided", http.StatusUnauthorized)
			return
		}

		// Validate Token
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			// 1. Check for HMAC (HS256) - Standard Supabase Token
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); ok {
				jwtSecret := os.Getenv("SUPABASE_JWT_SECRET")
				if jwtSecret == "" {
					log.Println("FATAL: SUPABASE_JWT_SECRET environment variable not set.")
					return nil, fmt.Errorf("server is not configured to validate JWTs")
				}
				return []byte(jwtSecret), nil
			}

			// 2. Check for ECDSA (ES256) - Fetch Public Key from Supabase JWKS
			if _, ok := token.Method.(*jwt.SigningMethodECDSA); ok {
				kid, ok := token.Header["kid"].(string)
				if !ok {
					log.Println("ERROR: Token header missing 'kid'")
					return nil, fmt.Errorf("missing 'kid' header in token")
				}
				key, err := getSupabasePublicKey(kid)
				if err != nil {
					log.Printf("ERROR: Failed to get public key for kid %s: %v", kid, err)
					return nil, err
				}
				return key, nil
			}

			log.Printf("ERROR: Unexpected signing method: %v", token.Header["alg"])
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		})

		if err != nil || !token.Valid {
			log.Printf("Invalid token: %v", err)
			http.Error(w, "Unauthorized: Invalid or expired token", http.StatusUnauthorized)
			return
		}

		// Extract the user_id (the 'sub' claim in Supabase JWTs)
		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			log.Println("ERROR: Could not parse token claims")
			http.Error(w, "Unauthorized: Could not parse token claims", http.StatusUnauthorized)
			return
		}
		// It extracts the user ID (the 'sub' claim) from the token.
		userID, ok := claims["sub"].(string)
		if !ok {
			log.Println("ERROR: User ID (sub) claim is missing or invalid")
			http.Error(w, "Unauthorized: User ID (sub) claim is missing or invalid", http.StatusUnauthorized)
			return
		}
		// If the token is valid and the user ID is found, it adds the userID to the request's context.
		// The request is then passed to the next handler in the chain (our wsHandler from main.go).
		ctx := context.WithValue(r.Context(), UserIDKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
