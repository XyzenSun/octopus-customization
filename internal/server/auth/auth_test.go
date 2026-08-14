package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/golang-jwt/jwt/v5"
)

func setupAuthTest(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", t.TempDir()+"/auth.db", false); err != nil {
		t.Fatalf("init test database: %v", err)
	}
	if err := op.UserInit(); err != nil {
		t.Fatalf("init test user: %v", err)
	}
	conf.AppConfig.AdminCookieSecure = true
}

func TestGenerateJWTTokenUsesFixedSevenDayLifetime(t *testing.T) {
	setupAuthTest(t)

	tokenString, err := GenerateJWTToken()
	if err != nil {
		t.Fatalf("GenerateJWTToken() error: %v", err)
	}

	claims := &jwt.RegisteredClaims{}
	if _, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		user := op.UserGet()
		return []byte(user.Username + user.Password), nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(conf.APP_NAME),
		jwt.WithAudience(AdminSessionAudience),
	); err != nil {
		t.Fatalf("parse generated token: %v", err)
	}
	if claims.ExpiresAt == nil || claims.IssuedAt == nil {
		t.Fatal("generated token is missing issued-at or expiration")
	}

	lifetime := claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time)
	if lifetime != AdminSessionTTL {
		t.Fatalf("token lifetime = %s, want %s", lifetime, AdminSessionTTL)
	}
	if !VerifyJWTToken(tokenString) {
		t.Fatal("VerifyJWTToken rejected a generated token")
	}
}

func TestAdminSessionCookieSecurityAttributes(t *testing.T) {
	conf.AppConfig.AdminCookieSecure = true
	recorder := httptest.NewRecorder()

	SetAdminSessionCookie(recorder, "signed-token")
	result := recorder.Result()
	cookies := result.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookie count = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != AdminSessionCookieName || cookie.Value != "signed-token" {
		t.Fatalf("unexpected cookie: %#v", cookie)
	}
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie security attributes are incomplete: %#v", cookie)
	}
	if cookie.Path != AdminSessionCookiePath || cookie.MaxAge != int(AdminSessionTTL/time.Second) {
		t.Fatalf("cookie scope or lifetime is incorrect: %#v", cookie)
	}
	if !strings.Contains(result.Header.Get("Set-Cookie"), "SameSite=Strict") {
		t.Fatalf("Set-Cookie does not contain SameSite=Strict: %s", result.Header.Get("Set-Cookie"))
	}
}

func TestClearAdminSessionCookie(t *testing.T) {
	conf.AppConfig.AdminCookieSecure = true
	recorder := httptest.NewRecorder()

	ClearAdminSessionCookie(recorder)
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge != -1 || cookies[0].Value != "" {
		t.Fatalf("clear cookie is invalid: %#v", cookies)
	}
}
