package password

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/neghi-go/auth"
	"github.com/neghi-go/auth/internal/models"
	"github.com/neghi-go/auth/jwt"
	"github.com/neghi-go/database"
	"github.com/neghi-go/utilities"
	"golang.org/x/crypto/argon2"
)

type Action string

const (
	verify Action = "verify"
	resend Action = "resend"
	reset  Action = "reset"
)

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type resetPasswordRequest struct {
	Email       string `json:"email"`
	Token       string `json:"token"`
	NewPassword string `json:"password"`
}

type verifyEmailRequest struct {
	Email string `json:"email"`
	Token string `json:"token"`
}

type Option func(*PasswordProviderConfig)

type PasswordProviderConfig struct {
	issuer   string
	audience string
	hash     Hasher
	store    database.Model[models.User]
	notify   func(email, token string) error
	jwt      *jwt.JWT
}

func Config(opts ...Option) *PasswordProviderConfig {
	cfg := &PasswordProviderConfig{
		issuer:   "demo-issuer",
		audience: "demo-audience",
		hash:     &argonHasher{},
	}

	for _, opt := range opts {
		opt(cfg)
	}

	return cfg
}

func withJWT(jwt *jwt.JWT) Option {
	return func(ppc *PasswordProviderConfig) {
		ppc.jwt = jwt
	}
}

func withModel(userModel database.Model[models.User]) Option {
	return func(ppc *PasswordProviderConfig) {
		ppc.store = userModel
	}
}

func withNotifier(notify func(email, token string) error) Option {
	return func(ppc *PasswordProviderConfig) {
		ppc.notify = notify
	}
}

func New(cfg *PasswordProviderConfig) *auth.Provider {
	return &auth.Provider{
		Type: "password",
		Init: func(r chi.Router) {
			r.Post("/login", func(w http.ResponseWriter, r *http.Request) {
				action := r.URL.Query().Get("action")
				var body loginRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					utilities.JSON(w).
						SetStatus(utilities.ResponseError).
						SetStatusCode(http.StatusInternalServerError).
						SetMessage(err.Error()).
						Send()
					return
				}
				//fetch user
				user, err := cfg.store.WithContext(r.Context()).
					Filter(database.SetParams(database.SetFilter("email", body.Email))).
					FindFirst()
				if err != nil {
					utilities.JSON(w).
						SetStatus(utilities.ResponseFail).
						SetStatusCode(http.StatusBadRequest).
						SetMessage(err.Error()).
						Send()
					return
				}

				//check if user email is verified
				if !user.EmailVerified {
					action = string(verify)
				}

				switch Action(action) {
				case verify:
					token := utilities.Generate(4)

					user.EmailVerifyToken = token
					user.EmailVerifyTokenExpiresAt = time.Now().Add(time.Hour * 2).UTC()

					if err := cfg.store.WithContext(r.Context()).Filter(database.SetParams(database.SetFilter("email", user.Email))).
						UpdateOne(*user); err != nil {
						utilities.JSON(w).
							SetStatus(utilities.ResponseFail).
							SetStatusCode(http.StatusBadRequest).
							SetMessage(err.Error()).
							Send()
						return
					}
					if err := cfg.notify(user.Email, token); err != nil {
						utilities.JSON(w).
							SetStatus(utilities.ResponseFail).
							SetStatusCode(http.StatusBadRequest).
							SetMessage(err.Error()).
							Send()
						return
					}
					utilities.JSON(w).
						SetStatus(utilities.ResponseSuccess).
						SetStatusCode(http.StatusOK).
						SetMessage("your email is yet to be verified, please check your email for verification code").
						Send()

				default:
					//validate Password
					if err := cfg.hash.compare(body.Password, user.PasswordSalt, user.Password); err != nil {
						utilities.JSON(w).
							SetStatus(utilities.ResponseFail).
							SetStatusCode(http.StatusBadRequest).
							SetMessage(err.Error()).
							Send()
						return
					}
					user.LastLogin = time.Now().UTC().Unix()
					if err := cfg.store.WithContext(r.Context()).Filter(database.SetParams(database.SetFilter("email", user.Email))).
						UpdateOne(*user); err != nil {
						utilities.JSON(w).
							SetStatus(utilities.ResponseFail).
							SetStatusCode(http.StatusBadRequest).
							SetMessage(err.Error()).
							Send()
						return
					}

					//create session, either JWT or Cookie and send to user
					jwtToken, err := cfg.jwt.Sign(*jwt.JWTClaims(
						jwt.SetIssuer(cfg.issuer),
						jwt.SetAudience(cfg.audience),
						jwt.SetSubject(user.ID.String()),
						jwt.SetExpiration(time.Now().Add(time.Hour*24*30)),
					))
					if err != nil {
						utilities.JSON(w).
							SetStatus(utilities.ResponseFail).
							SetStatusCode(http.StatusBadRequest).
							SetMessage(err.Error()).
							Send()
						return
					}
					utilities.JSON(w).
						SetStatus(utilities.ResponseSuccess).
						SetStatusCode(http.StatusOK).
						SetMessage("successfull login attempt").
						SetData(map[string]interface{}{
							"user":  user,
							"token": string(jwtToken),
						}).
						Send()
				}

			})
			r.Post("/register", func(w http.ResponseWriter, r *http.Request) {
				var body registerRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					utilities.JSON(w).
						SetStatus(utilities.ResponseError).
						SetStatusCode(http.StatusInternalServerError).
						SetMessage(err.Error() + "unable to get user details").
						Send()
					return
				}

				//validate user data.

				//store validated user
				user := models.User{
					ID:           uuid.New(),
					Email:        body.Email,
					PasswordSalt: utilities.Generate(16),

					EmailVerifyToken:          utilities.Generate(4),
					EmailVerifyTokenExpiresAt: time.Now().Add(time.Hour * 2).UTC(),
				}

				//hash passwords
				hashedPassword := cfg.hash.hash(body.Password, user.PasswordSalt)
				user.Password = hashedPassword

				//persist user data
				if err := cfg.store.WithContext(r.Context()).Save(user); err != nil {
					utilities.JSON(w).
						SetStatus(utilities.ResponseError).
						SetStatusCode(http.StatusInternalServerError).
						SetMessage(err.Error()).
						Send()
					return
				}

				//send notification with token
				if err := cfg.notify(user.Email, user.EmailVerifyToken); err != nil {
					utilities.JSON(w).
						SetStatus(utilities.ResponseFail).
						SetStatusCode(http.StatusBadRequest).
						SetMessage(err.Error()).
						Send()
					return
				}
				utilities.JSON(w).
					SetStatus(utilities.ResponseSuccess).
					SetStatusCode(http.StatusOK).
					SetMessage("your verification code has been resent").
					Send()

			})
			r.Post("/password-reset", func(w http.ResponseWriter, r *http.Request) {
				action := r.URL.Query().Get("action")
				var body resetPasswordRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					utilities.JSON(w).
						SetStatus(utilities.ResponseError).
						SetStatusCode(http.StatusInternalServerError).
						SetMessage(err.Error()).
						Send()
					return
				}
				user, err := cfg.store.WithContext(r.Context()).
					Filter(database.SetParams(database.SetFilter("email", body.Email))).FindFirst()
				if err != nil {
					utilities.JSON(w).
						SetStatus(utilities.ResponseFail).
						SetStatusCode(http.StatusBadRequest).
						SetMessage(err.Error()).
						Send()
					return
				}

				switch Action(action) {
				case reset:
					token := utilities.Generate(6)
					user.PasswordResetToken = token
					user.PasswordResetTokenExpiresAt = time.Now().Add(time.Hour * 1).UTC()

					if err := cfg.store.WithContext(r.Context()).Filter(database.SetParams(database.SetFilter("email", user.Email))).
						UpdateOne(*user); err != nil {
						utilities.JSON(w).
							SetStatus(utilities.ResponseFail).
							SetStatusCode(http.StatusBadRequest).
							SetMessage(err.Error()).
							Send()
						return
					}

					if err := cfg.notify(user.Email, token); err != nil {
						utilities.JSON(w).
							SetStatus(utilities.ResponseFail).
							SetStatusCode(http.StatusBadRequest).
							SetMessage(err.Error()).
							Send()
						return
					}
					utilities.JSON(w).
						SetStatus(utilities.ResponseSuccess).
						SetStatusCode(http.StatusOK).
						SetMessage("your reset code has been sent").
						Send()
				default:
					if body.Token != user.PasswordResetToken {
						utilities.JSON(w).
							SetStatus(utilities.ResponseFail).
							SetStatusCode(http.StatusBadRequest).
							SetMessage("invalid reset token").
							Send()
						return
					}
					if time.Now().UTC().Unix() > user.PasswordResetTokenExpiresAt.Unix() {
						user.PasswordResetToken = utilities.Generate(6)
						user.PasswordResetTokenExpiresAt = time.Now().Add(time.Hour * 1).UTC()
						if err := cfg.store.WithContext(r.Context()).Filter(database.SetParams(database.SetFilter("email", user.Email))).
							UpdateOne(*user); err != nil {
							utilities.JSON(w).
								SetStatus(utilities.ResponseFail).
								SetStatusCode(http.StatusBadRequest).
								SetMessage(err.Error()).
								Send()
							return
						}
						if err := cfg.notify(user.Email, user.EmailVerifyToken); err != nil {
							utilities.JSON(w).
								SetStatus(utilities.ResponseFail).
								SetStatusCode(http.StatusBadRequest).
								SetMessage(err.Error()).
								Send()
							return
						}
						utilities.JSON(w).
							SetStatus(utilities.ResponseFail).
							SetStatusCode(http.StatusBadRequest).
							SetMessage("token is expired, a new one has been sent to your mail").
							Send()
						return
					}

					user.PasswordResetToken = ""
					user.PasswordSalt = utilities.Generate(16)
					user.Password = cfg.hash.hash(body.NewPassword, user.PasswordSalt)
					user.PasswordUpdatedOn = time.Now().UTC()

					if err := cfg.store.WithContext(r.Context()).Filter(database.SetParams(database.SetFilter("email", user.Email))).
						UpdateOne(*user); err != nil {
						utilities.JSON(w).
							SetStatus(utilities.ResponseFail).
							SetStatusCode(http.StatusBadRequest).
							SetMessage(err.Error()).
							Send()
						return
					}

					utilities.JSON(w).
						SetStatus(utilities.ResponseSuccess).
						SetStatusCode(http.StatusOK).
						SetMessage("password changed, redirect to login").
						Send()
				}
			})
			r.Post("/email-verify", func(w http.ResponseWriter, r *http.Request) {
				action := r.URL.Query().Get("action")
				var body verifyEmailRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					utilities.JSON(w).
						SetStatus(utilities.ResponseError).
						SetStatusCode(http.StatusInternalServerError).
						SetMessage(err.Error()).
						Send()
					return
				}

				user, err := cfg.store.WithContext(r.Context()).
					Filter(database.SetParams(database.SetFilter("email", body.Email))).
					FindFirst()
				if err != nil {
					fmt.Print("here!")
					utilities.JSON(w).
						SetStatus(utilities.ResponseFail).
						SetStatusCode(http.StatusBadRequest).
						SetMessage(err.Error()).
						Send()
					return
				}
				switch Action(action) {
				case resend:
					token := utilities.Generate(4)

					user.EmailVerifyToken = token
					user.EmailVerifyTokenExpiresAt = time.Now().Add(time.Hour * 2).UTC()

					if err := cfg.store.WithContext(r.Context()).Filter(database.SetParams(database.SetFilter("email", user.Email))).
						UpdateOne(*user); err != nil {
						utilities.JSON(w).
							SetStatus(utilities.ResponseFail).
							SetStatusCode(http.StatusBadRequest).
							SetMessage(err.Error()).
							Send()
						return
					}
					if err := cfg.notify(user.Email, token); err != nil {
						utilities.JSON(w).
							SetStatus(utilities.ResponseFail).
							SetStatusCode(http.StatusBadRequest).
							SetMessage(err.Error()).
							Send()
						return
					}
					utilities.JSON(w).
						SetStatus(utilities.ResponseSuccess).
						SetStatusCode(http.StatusOK).
						SetMessage("your verification code has been resent").
						Send()
				default:
					if body.Token != user.EmailVerifyToken {
						utilities.JSON(w).
							SetStatus(utilities.ResponseFail).
							SetStatusCode(http.StatusBadRequest).
							SetMessage("invalid verification token").
							Send()
						return
					}
					if time.Now().UTC().Unix() > user.EmailVerifyTokenExpiresAt.Unix() {
						user.EmailVerifyToken = utilities.Generate(4)
						user.EmailVerifyTokenExpiresAt = time.Now().Add(time.Hour * 2).UTC()
						if err := cfg.store.WithContext(r.Context()).Filter(database.SetParams(database.SetFilter("email", user.Email))).
							UpdateOne(*user); err != nil {
							utilities.JSON(w).
								SetStatus(utilities.ResponseFail).
								SetStatusCode(http.StatusBadRequest).
								SetMessage(err.Error()).
								Send()
							return
						}
						if err := cfg.notify(user.Email, user.EmailVerifyToken); err != nil {
							utilities.JSON(w).
								SetStatus(utilities.ResponseFail).
								SetStatusCode(http.StatusBadRequest).
								SetMessage(err.Error()).
								Send()
							return
						}
						utilities.JSON(w).
							SetStatus(utilities.ResponseFail).
							SetStatusCode(http.StatusBadRequest).
							SetMessage("token is expired, a new one has been sent to your mail").
							Send()
						return
					}

					user.EmailVerifyToken = ""
					user.EmailVerified = true
					user.EmailVerifyTokenExpiresAt = time.Time{}
					user.EmailVerifiedAt = time.Now().UTC()
					if err := cfg.store.WithContext(r.Context()).Filter(database.SetParams(database.SetFilter("email", user.Email))).
						UpdateOne(*user); err != nil {
						utilities.JSON(w).
							SetStatus(utilities.ResponseFail).
							SetStatusCode(http.StatusBadRequest).
							SetMessage(err.Error()).
							Send()
						return
					}

					utilities.JSON(w).
						SetStatus(utilities.ResponseSuccess).
						SetStatusCode(http.StatusOK).
						SetMessage("email successfully verified, redirect to login").
						Send()
				}
			})
		},
	}
}

type Hasher interface {
	hash(password string, salt string) string
	compare(password, salt, compare string) error
}

type argonHasher struct{}

func (a *argonHasher) hash(password string, salt string) string {
	return string(argon2.IDKey([]byte(password), []byte(salt), 2, 19*1024, 1, 32))
}
func (a *argonHasher) compare(password, salt, compare string) error {
	pass := argon2.IDKey([]byte(password), []byte(salt), 2, 19*1024, 1, 32)
	if subtle.ConstantTimeCompare(pass, []byte(compare)) != 1 {
		return errors.New("passwords don't Match")
	}
	return nil
}
