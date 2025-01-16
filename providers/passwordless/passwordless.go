package passwordless

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/neghi-go/auth"
	"github.com/neghi-go/auth/internal/models"
	"github.com/neghi-go/database"
	"github.com/neghi-go/utilities"
)

type Action string

const (
	authenticate Action = "authenticate"
	resend       Action = "resend"
)

type authenticateRequest struct {
	Email string `json:"email"`
}

type PasswordlessProviderConfig struct {
	store   database.Model[models.User]
	notify  func(email string, token string) error
	encrypt *auth.Encrypt
}

func New(cfg *PasswordlessProviderConfig) *auth.Provider {
	return &auth.Provider{
		Type: "magic-link",
		Init: func(r chi.Router) {
			r.Post("/magic-link/authorize", authorize(cfg))
		},
	}
}

func authorize(cfg *PasswordlessProviderConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body authenticateRequest
		res := utilities.JSON(w)
		action := r.URL.Query().Get("action")
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			res.SetStatus(utilities.ResponseError).
				SetStatusCode(http.StatusInternalServerError).
				Send()
			return
		}

		switch Action(action) {
		case authenticate:
			token := r.URL.Query().Get("token")

			if token == "" {
				res.SetStatus(utilities.ResponseError).
					SetStatusCode(http.StatusBadRequest).
					Send()
				return
			}
			//when url contains token
			//parse token
			tokenValues, _ := cfg.encrypt.Decrypt(token)
			//validate parsed token
			if tokenValues["email"] != body.Email {
				res.SetStatus(utilities.ResponseError).
					SetStatusCode(http.StatusBadRequest).
					Send()
				return
			}
			timestamp, _ := tokenValues["expiry"].(int64)
			if time.Now().UTC().Unix() > timestamp {
				res.SetStatus(utilities.ResponseError).
					SetStatusCode(http.StatusBadRequest).
					SetMessage("validation token has expire, please try again").
					Send()
				return
			}

			user, err := cfg.store.WithContext(r.Context()).
				Filter(database.SetParams(database.SetFilter("email", body.Email))).First()
			if err != nil {
				res.SetStatus(utilities.ResponseError).
					SetStatusCode(http.StatusBadRequest).
					SetMessage("user not found").
					Send()
				return
			}
			user.LastLogin = time.Now().UTC().Unix()
			if !user.EmailVerified {
				user.EmailVerified = true
			}
			// create session for user

			//update user
			if err := cfg.store.WithContext(r.Context()).Filter(database.SetParams(database.SetFilter("email", body.Email))).
				UpdateOne(*user); err != nil {
				res.SetStatus(utilities.ResponseError).
					SetStatusCode(http.StatusBadRequest).
					SetMessage("user not updated").
					Send()
				return

			}
			res.SetStatus(utilities.ResponseSuccess).
				SetStatusCode(http.StatusOK).
				Send()
		case resend:
			if _, err := cfg.store.WithContext(r.Context()).
				Filter(database.SetParams(database.SetFilter("email", body.Email))).First(); err != nil {
				res.SetStatus(utilities.ResponseError).
					SetStatusCode(http.StatusBadRequest).
					SetMessage("user not found").
					Send()
				return
			}
			tokenValues := map[string]any{
				"email":  body.Email,
				"expiry": time.Now().Add(time.Minute * 10).UTC().Unix(),
			}
			token, _ := cfg.encrypt.Encrypt(tokenValues)
			// send to user
			_ = cfg.notify(body.Email, token)
			res.SetStatus(utilities.ResponseSuccess).
				SetStatusCode(http.StatusOK).
				Send()
		default:
			//get user if it exist and generate session
			if _, err := cfg.store.WithContext(r.Context()).
				Filter(database.SetParams(database.SetFilter("email", body.Email))).First(); err != nil {
				if errors.Is(err, errors.New("doc not found")) {
					if err := cfg.store.WithContext(r.Context()).Save(models.User{
						Email: body.Email,
					}); err != nil {
						res.SetStatus(utilities.ResponseError).
							SetStatusCode(http.StatusBadRequest).
							Send()
						return
					}
				}
			}
			tokenValues := map[string]any{
				"email":  body.Email,
				"expiry": time.Now().Add(time.Minute * 10).UTC().Unix(),
			}
			token, _ := cfg.encrypt.Encrypt(tokenValues)
			_ = cfg.notify(body.Email, token)

			res.SetStatus(utilities.ResponseSuccess).
				SetStatusCode(http.StatusOK).
				SetMessage("please check your email for authentication link").
				Send()
		}
	}
}
