package middlewares

import (
	"context"
	"net/http"
	"strings"

	"github.com/rajandhamala/Maruvo/internal/utils"
)

func Auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")

		scheme, token, ok := strings.Cut(authHeader, " ")
		if !ok || scheme != "Bearer" || token == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		user, err := utils.VerifyUserToken(token)
		if err != nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), utils.UserKey, user)

		next(w, r.WithContext(ctx))
	}
}
