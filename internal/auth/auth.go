package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/agentic/mcp-proxy/internal/models"
	"github.com/agentic/mcp-proxy/internal/store"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// AuthService handles authentication (JWT + API keys).
type AuthService struct {
	store     *store.Store
	jwtSecret []byte
}

// New creates a new AuthService.
func New(s *store.Store, jwtSecret string) *AuthService {
	return &AuthService{
		store:     s,
		jwtSecret: []byte(jwtSecret),
	}
}

// JWTSecret returns the JWT secret as a string.
func (a *AuthService) JWTSecret() string {
	return string(a.jwtSecret)
}

// HashPassword hashes a password using bcrypt.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword checks a password against a bcrypt hash.
func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// GenerateToken creates a JWT token for a user.
func (a *AuthService) GenerateToken(userID, username, role string) (string, time.Time, error) {
	expiresAt := time.Now().Add(24 * time.Hour)
	claims := jwt.MapClaims{
		"user_id":  userID,
		"username": username,
		"role":     role,
		"exp":      expiresAt.Unix(),
		"iat":      time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(a.jwtSecret)
	if err != nil {
		return "", time.Time{}, err
	}
	return tokenString, expiresAt, nil
}

// ValidateToken validates a JWT token and returns the claims.
func (a *AuthService) ValidateToken(tokenString string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return a.jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		return claims, nil
	}
	return nil, fmt.Errorf("invalid token")
}

// GenerateAPIKey creates a new API key string and stores its hash.
func (a *AuthService) GenerateAPIKey() (string, string, string, error) {
	// Generate a random key
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return "", "", "", err
	}
	keyString := "mcp_" + hex.EncodeToString(keyBytes)
	keyPrefix := keyString[:12] + "..."

	// Hash the key for storage
	hash := sha256.Sum256([]byte(keyString))
	keyHash := hex.EncodeToString(hash[:])

	return keyString, keyHash, keyPrefix, nil
}

// ValidateAPIKey checks an API key and returns the associated key record.
func (a *AuthService) ValidateAPIKey(keyString string) (*models.APIKey, error) {
	hash := sha256.Sum256([]byte(keyString))
	keyHash := hex.EncodeToString(hash[:])

	apiKey, err := a.store.GetAPIKeyByHash(keyHash)
	if err != nil || apiKey == nil {
		return nil, fmt.Errorf("invalid API key")
	}

	// Check expiration
	if apiKey.ExpiresAt != nil && time.Now().After(*apiKey.ExpiresAt) {
		return nil, fmt.Errorf("API key expired")
	}

	// Update last used
	_ = a.store.UpdateAPIKeyLastUsed(apiKey.ID)

	return apiKey, nil
}

// HasScope checks if the API key has a required scope.
func HasScope(scopes []string, required string) bool {
	for _, s := range scopes {
		if s == required || s == "admin" {
			return true
		}
	}
	return false
}

// ExtractToken extracts the Bearer token from the Authorization header.
func ExtractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

// ExtractAPIKey extracts the API key from the header or query param.
func ExtractAPIKey(r *http.Request) string {
	// Check X-API-Key header first
	if key := r.Header.Get("X-API-Key"); key != "" {
		return key
	}
	// Check Authorization header with Bearer scheme
	if key := ExtractToken(r); key != "" && strings.HasPrefix(key, "mcp_") {
		return key
	}
	// Check query parameter
	if key := r.URL.Query().Get("api_key"); key != "" {
		return key
	}
	return ""
}

// JWTMiddleware protects routes requiring admin JWT auth.
func (a *AuthService) JWTMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenString := ExtractToken(r)
		if tokenString == "" {
			writeAuthError(w, "Missing or invalid Authorization header")
			return
		}

		claims, err := a.ValidateToken(tokenString)
		if err != nil {
			writeAuthError(w, "Invalid or expired token")
			return
		}

		// Inject claims into request context
		ctx := WithClaims(r.Context(), claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// APIKeyMiddleware protects MCP proxy routes requiring API key auth.
func (a *AuthService) APIKeyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keyString := ExtractAPIKey(r)
		if keyString == "" {
			writeAuthError(w, "Missing API key")
			return
		}

		apiKey, err := a.ValidateAPIKey(keyString)
		if err != nil {
			writeAuthError(w, "Invalid or expired API key")
			return
		}

		ctx := WithAPIKey(r.Context(), apiKey)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AdminUserFromContext extracts the admin user info from JWT claims in context.
func AdminUserFromContext(r *http.Request) string {
	claims, ok := r.Context().Value(claimsKey{}).(jwt.MapClaims)
	if !ok {
		return ""
	}
	if username, ok := claims["username"].(string); ok {
		return username
	}
	return ""
}

func writeAuthError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// EnsureDefaultAdmin creates a default admin user if none exists.
func (a *AuthService) EnsureDefaultAdmin(username, password string) error {
	_, err := a.store.GetUserByUsername(username)
	if err == nil {
		// User exists, update password if needed
		return nil
	}

	hash, err := HashPassword(password)
	if err != nil {
		return err
	}

	user := &models.User{
		ID:           generateID("usr"),
		Username:     username,
		PasswordHash: hash,
		Role:         "admin",
		CreatedAt:    time.Now(),
	}
	return a.store.CreateUser(user)
}

// generateID creates a short unique ID with a prefix.
func generateID(prefix string) string {
	b := make([]byte, 8)
	rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

// ConstantTimeCompare is re-exported for convenience.
var _ = subtle.ConstantTimeCompare
var _ = os.Getenv
