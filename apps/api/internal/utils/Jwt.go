package utils

import (
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type UserJWT struct {
	ID string
}

type tokenClaims struct {
	ID string `json:"id"`
	// Username string `json:"username"`
	jwt.RegisteredClaims
}

func CreateUserToken(id string) (string, *UserJWT, error) {
	claims := tokenClaims{
		ID: id,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(3 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(os.Getenv("JWT_TOKEN")))
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
	return &UserJWT{ID: claims.ID}, nil
}
