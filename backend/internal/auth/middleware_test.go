package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMiddleware(t *testing.T) {
	secret := "test-secret-key-at-least-32-bytes-long"
	signer, err := NewSigner(secret, 24*time.Hour)
	if err != nil {
		t.Fatalf("NewSigner failed: %v", err)
	}

	// Create a test handler that checks claims
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetClaims(r.Context())
		if claims == nil {
			t.Error("Expected claims in context, got nil")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if claims.UserID != "testuser" {
			t.Errorf("Expected UserID 'testuser', got '%s'", claims.UserID)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	middleware := Middleware(signer, testHandler)

	t.Run("valid token", func(t *testing.T) {
		token, err := signer.Sign("testuser", "admin")
		if err != nil {
			t.Fatalf("Sign failed: %v", err)
		}

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()

		middleware.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}
	})

	t.Run("missing authorization header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()

		middleware.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401, got %d", w.Code)
		}
	})

	t.Run("invalid bearer format", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Authorization", "InvalidFormat token")
		w := httptest.NewRecorder()

		middleware.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401, got %d", w.Code)
		}
	})

	t.Run("empty token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Authorization", "Bearer ")
		w := httptest.NewRecorder()

		middleware.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401, got %d", w.Code)
		}
	})

	t.Run("invalid token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Authorization", "Bearer invalid.token.here")
		w := httptest.NewRecorder()

		middleware.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401, got %d", w.Code)
		}
	})

	t.Run("expired token", func(t *testing.T) {
		shortSigner, err := NewSigner(secret, 1*time.Millisecond)
		if err != nil {
			t.Fatalf("NewSigner failed: %v", err)
		}
		token, err := shortSigner.Sign("testuser", "admin")
		if err != nil {
			t.Fatalf("Sign failed: %v", err)
		}

		time.Sleep(10 * time.Millisecond)

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()

		shortMiddleware := Middleware(shortSigner, testHandler)
		shortMiddleware.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401, got %d", w.Code)
		}
	})
}

func TestGetClaims(t *testing.T) {
	secret := "test-secret-key-at-least-32-bytes-long"
	signer, err := NewSigner(secret, 24*time.Hour)
	if err != nil {
		t.Fatalf("NewSigner failed: %v", err)
	}

	token, err := signer.Sign("testuser", "admin")
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	t.Run("claims present", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		handler := Middleware(signer, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil {
				t.Error("Expected claims, got nil")
				return
			}
			if claims.UserID != "testuser" {
				t.Errorf("Expected UserID 'testuser', got '%s'", claims.UserID)
			}
			w.WriteHeader(http.StatusOK)
		}))

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	})

	t.Run("claims absent", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		claims := GetClaims(req.Context())
		if claims != nil {
			t.Error("Expected nil claims without middleware, got non-nil")
		}
	})
}
