package password

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/neghi-go/auth"
	"github.com/neghi-go/auth/storage"
	_ "golang.org/x/crypto/argon2"
	_ "golang.org/x/crypto/bcrypt"
	_ "golang.org/x/crypto/scrypt"
)

type Option func(*PasswordProviderConfig)

type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Password  string    `json:"password"`
	LastLogin time.Time `json:"last_login"`
}

type PasswordProviderConfig struct {
	hash  Hasher
	store storage.Store
}

func Config(opts ...Option) *PasswordProviderConfig {
	cfg := &PasswordProviderConfig{}

	for _, opt := range opts {
		opt(cfg)
	}

	return cfg
}

func New(cfg *PasswordProviderConfig) *auth.Provider {
	return &auth.Provider{
		Type: "password",
		Init: func(r chi.Router) {
			r.Post("/password/login", login())
			r.Post("/password/register", register())
			r.Post("/password/reset-password", reset())
			r.Post("/logout", logout())
		},
	}
}

func login() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {}
}

func register() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {}
}

func logout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {}
}

func reset() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {}
}

type Hasher interface {
	hash(password string, salt int) (string, error)
	compare(password, compare string) error
}

type ArgonHasher struct {
}

func (a *ArgonHasher) hash(password string, salt int) (string, error) {
	return "", nil
}
func (a *ArgonHasher) compare(password string, compare string) error {
	return nil
}
