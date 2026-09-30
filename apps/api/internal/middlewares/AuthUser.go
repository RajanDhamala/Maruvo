package middlewares

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/rajandhamala/Maruvo/internal/utils"

	"github.com/google/uuid"
)

func Auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("client_id"); err == nil {
			claims, err := utils.VerifyUserToken(cookie.Value)

			if err == nil {
				ctx := context.WithValue(
					r.Context(),
					utils.UserKey,
					claims,
				)

				next(w, r.WithContext(ctx))
				return
			}
		}

		NewUserId := uuid.NewString()

		NewToken, Claims, err := utils.CreateUserToken(NewUserId)
		if err != nil {
			http.Error(w, "failed to create token", http.StatusInternalServerError)
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     "client_id",
			Value:    NewToken,
			HttpOnly: true,
			Secure:   os.Getenv("COOKIE_SECURE") == "true",

			SameSite: http.SameSiteLaxMode,
			Path:     "/",
			Expires:  time.Now().Add(24 * 3 * time.Hour),
		})

		ctx := context.WithValue(
			r.Context(),
			utils.UserKey,
			Claims,
		)
		next(w, r.WithContext(ctx))
		return
	}
}
