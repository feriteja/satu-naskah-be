package middleware

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const UserIDKey contextKey = "userID"

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// For WebSockets, tokens are often passed in the query string
		// because the browser's WebSocket API doesn't support custom headers.
		tokenString := r.URL.Query().Get("token")

		// Fallback to Header if you're testing via Postman/CURL
		if tokenString == "" {
			authHeader := r.Header.Get("Authorization")
			tokenString = strings.TrimPrefix(authHeader, "Bearer ")
		}

		if tokenString == "" {
			http.Error(w, "Unauthorized: No token provided", http.StatusUnauthorized)
			return
		}

		// Validate Token
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			// Ensure the signing method is HMAC (Supabase default)
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			jwtSecret := os.Getenv("SUPABASE_JWT_SECRET")
			if jwtSecret == "" {
				log.Println("FATAL: SUPABASE_JWT_SECRET environment variable not set.")
				return nil, fmt.Errorf("server is not configured to validate JWTs")
			}
			return []byte(jwtSecret), nil
		})

		if err != nil || !token.Valid {
			log.Printf("Invalid token: %v", err)
			http.Error(w, "Unauthorized: Invalid or expired token", http.StatusUnauthorized)
			return
		}

		// Extract the user_id (the 'sub' claim in Supabase JWTs)
		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			http.Error(w, "Unauthorized: Could not parse token claims", http.StatusUnauthorized)
			return
		}
		userID, ok := claims["sub"].(string)
		if !ok {
			http.Error(w, "Unauthorized: User ID (sub) claim is missing or invalid", http.StatusUnauthorized)
			return
		}
		// Add UserID to context for the next handler
		ctx := context.WithValue(r.Context(), UserIDKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
