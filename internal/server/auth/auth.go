package auth

import (
	"crypto/rand"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/golang-jwt/jwt/v5"
)

const (
	AdminSessionCookieName = "octopus_admin_session"
	AdminSessionCookiePath = "/api/v1"
	AdminSessionAudience   = "octopus-admin"
	AdminSessionTTL        = 7 * 24 * time.Hour
)

func GenerateJWTToken() (string, error) {
	now := time.Now()
	user := op.UserGet()
	claims := &jwt.RegisteredClaims{
		Subject:   strconv.FormatUint(uint64(user.ID), 10),
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		Issuer:    conf.APP_NAME,
		Audience:  jwt.ClaimStrings{AdminSessionAudience},
		ExpiresAt: jwt.NewNumericDate(now.Add(AdminSessionTTL)),
	}
	secret := user.Username + user.Password
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		return "", err
	}
	return token, nil
}

func VerifyJWTToken(token string) bool {
	user := op.UserGet()
	secret := user.Username + user.Password
	jwtToken, err := jwt.Parse(token, func(token *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(conf.APP_NAME),
		jwt.WithAudience(AdminSessionAudience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithSubject(strconv.FormatUint(uint64(user.ID), 10)),
	)
	return err == nil && jwtToken.Valid
}

func SetAdminSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     AdminSessionCookieName,
		Value:    token,
		Path:     AdminSessionCookiePath,
		MaxAge:   int(AdminSessionTTL / time.Second),
		Expires:  time.Now().Add(AdminSessionTTL),
		HttpOnly: true,
		Secure:   conf.AppConfig.AdminCookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

func ClearAdminSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     AdminSessionCookieName,
		Value:    "",
		Path:     AdminSessionCookiePath,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
		HttpOnly: true,
		Secure:   conf.AppConfig.AdminCookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

func GenerateAPIKey() string {
	const keyChars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := make([]byte, 48)
	maxI := big.NewInt(int64(len(keyChars)))
	for i := range b {
		n, err := rand.Int(rand.Reader, maxI)
		if err != nil {
			return ""
		}
		b[i] = keyChars[n.Int64()]
	}
	return "sk-" + conf.APP_NAME + "-" + string(b)
}
