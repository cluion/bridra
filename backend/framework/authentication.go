package framework

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
)

var ErrInvalidPrincipal = errors.New("framework: principal is invalid")
var ErrInvalidAuthenticator = errors.New("framework: authenticator is invalid")
var ErrAuthenticationFailed = errors.New("framework: authentication failed")

type Principal struct {
	subject     string
	permissions map[string]struct{}
}

func NewPrincipal(subject string, permissions ...string) (Principal, error) {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return Principal{}, ErrInvalidPrincipal
	}
	principal := Principal{
		subject:     subject,
		permissions: make(map[string]struct{}, len(permissions)),
	}
	for _, permission := range permissions {
		permission = strings.TrimSpace(permission)
		if permission == "" {
			return Principal{}, ErrInvalidPrincipal
		}
		principal.permissions[permission] = struct{}{}
	}
	return principal, nil
}

func (principal Principal) Subject() string {
	return principal.subject
}

func (principal Principal) HasPermission(permission string) bool {
	_, allowed := principal.permissions[permission]
	return allowed
}

func (principal Principal) valid() bool {
	return principal.subject != ""
}

type Authenticator interface {
	Authenticate(context.Context, string) (Principal, error)
}

type AuthenticatorFunc func(context.Context, string) (Principal, error)

func (authenticate AuthenticatorFunc) Authenticate(
	ctx context.Context,
	credential string,
) (Principal, error) {
	return authenticate(ctx, credential)
}

type staticTokenAuthenticator struct {
	expectedToken string
	principal     Principal
}

func NewStaticTokenAuthenticator(
	expectedToken string,
	principal Principal,
) (Authenticator, error) {
	if expectedToken == "" || !principal.valid() {
		return nil, ErrInvalidAuthenticator
	}
	return &staticTokenAuthenticator{
		expectedToken: expectedToken,
		principal:     principal,
	}, nil
}

func (authenticator *staticTokenAuthenticator) Authenticate(
	ctx context.Context,
	credential string,
) (Principal, error) {
	if err := ctx.Err(); err != nil {
		return Principal{}, err
	}
	if subtle.ConstantTimeCompare(
		[]byte(credential),
		[]byte(authenticator.expectedToken),
	) != 1 {
		return Principal{}, ErrAuthenticationFailed
	}
	return authenticator.principal, nil
}

type principalContextKey struct{}

func ContextWithPrincipal(parent context.Context, principal Principal) context.Context {
	if !principal.valid() {
		panic(ErrInvalidPrincipal)
	}
	return context.WithValue(parent, principalContextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	if ctx == nil {
		return Principal{}, false
	}
	principal, exists := ctx.Value(principalContextKey{}).(Principal)
	return principal, exists && principal.valid()
}

func RequirePermission(permission string) MethodPolicy {
	permission = strings.TrimSpace(permission)
	if permission == "" {
		panic("framework: permission cannot be empty")
	}
	return func(ctx *Context) error {
		principal, authenticated := PrincipalFromContext(ctx)
		if !authenticated {
			return NewError("unauthenticated", "Authentication is required.")
		}
		if !principal.HasPermission(permission) {
			return NewError("forbidden", "The authenticated principal is not allowed to call this method.")
		}
		return nil
	}
}
