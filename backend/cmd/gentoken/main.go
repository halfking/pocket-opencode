package main
import (
	"fmt"
	"os"
	"time"
	"github.com/golang-jwt/jwt/v5"
)
func main() {
	secret := os.Getenv("IDENTITY_SHARED_SECRET")
	if secret == "" {
		fmt.Fprintln(os.Stderr, "IDENTITY_SHARED_SECRET not set")
		os.Exit(1)
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"user_id": "testuser",
		"role": "admin",
		"workspace_id": "ws_test",
		"iss": "pocket",
		"aud": []string{"pocket-api"},
		"exp": now.Add(24 * time.Hour).Unix(),
		"iat": now.Unix(),
		"nbf": now.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to sign:", err)
		os.Exit(1)
	}
	fmt.Println(tokenString)
}
