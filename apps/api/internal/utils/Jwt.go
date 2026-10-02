package utils

import (
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type UserJWT struct {
	ID       string
	Username string
	Email    string
	GoogleId string
	Avtar    string
}

type tokenClaims struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	GoogleId string `json:"googleId"`
	Avtar    string `json:"avatar"`
	jwt.RegisteredClaims
}

func CreateUserToken(user *UserJWT) (string, *UserJWT, error) {
	claims := tokenClaims{
		ID:       user.ID,
		Username: user.Username,
		Email:    user.Email,
		GoogleId: user.GoogleId,
		Avtar:    user.Avtar,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(3 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).
		SignedString([]byte(os.Getenv("JWT_TOKEN")))
	if err != nil {
		return "", &UserJWT{}, err
	}

	data := UserJWT{ID: claims.ID}

	return token, &data, nil
}

func VerifyUserToken(tokenString string) (*UserJWT, error) {
	claims := &tokenClaims{}

	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method")
		}

		return []byte(os.Getenv("JWT_TOKEN")), nil
	})
	if err != nil || !token.Valid {
		return nil, fmt.Errorf("invalid access token")
	}

	return &UserJWT{
		ID:       claims.ID,
		Email:    claims.Email,
		GoogleId: claims.GoogleId,
		Avtar:    claims.Avtar,
		Username: claims.Username,
	}, nil
}
