package authentication

import (
	"github.com/go-chi/chi/v5"
	"github.com/neghi-go/iam/authentication/strategy"
	"github.com/neghi-go/iam/sessions"
	"github.com/neghi-go/iam/sessions/server"
)

type Options func(*Auth)

type Auth struct {
	providers []*strategy.Provider
	router    chi.Router
	session   sessions.Session
}

func New(r chi.Router, opts ...Options) *Auth {
	cfg := &Auth{
		router:  r,
		session: server.NewServerSession(),
	}
	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}

func RegisterStrategy(provider *strategy.Provider) Options {
	return func(a *Auth) {
		a.providers = append(a.providers, provider)
	}
}

func RegisterSession(session sessions.Session) Options {
	return func(a *Auth) {
		a.session = session
	}
}

func (a *Auth) Build() error {
	for _, p := range a.providers {
		p.Init(a.router, a.session)
	}
	return nil
}