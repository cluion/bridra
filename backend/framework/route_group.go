package framework

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidMiddlewareGroup        = errors.New("framework: middleware group is invalid")
	ErrMiddlewareGroupAlreadyDefined = errors.New("framework: middleware group is already defined")
	ErrMiddlewareGroupNotFound       = errors.New("framework: middleware group is not defined")
	ErrInvalidRouteGroup             = errors.New("framework: route group is invalid")
)

type RouteGroup struct {
	router      *Router
	prefix      string
	middlewares []Middleware
	policies    []MethodPolicy
}

func (r *Router) RegisterMiddlewareGroup(name string, middlewares ...Middleware) error {
	name = strings.TrimSpace(name)
	if name == "" || len(middlewares) == 0 {
		return ErrInvalidMiddlewareGroup
	}
	if err := validateMiddlewares(middlewares); err != nil {
		return err
	}
	if _, exists := r.middlewareGroups[name]; exists {
		return fmt.Errorf("%w: %s", ErrMiddlewareGroupAlreadyDefined, name)
	}
	r.middlewareGroups[name] = append([]Middleware(nil), middlewares...)
	return nil
}

func (r *Router) UseMiddlewareGroups(names ...string) error {
	middlewares, err := r.resolveMiddlewareGroups(names)
	if err != nil {
		return err
	}
	r.middlewares = append(r.middlewares, middlewares...)
	return nil
}

func (r *Router) Group(prefix string, middlewareGroups ...string) (*RouteGroup, error) {
	prefix, err := normalizeRoutePrefix(prefix)
	if err != nil {
		return nil, err
	}
	middlewares, err := r.resolveMiddlewareGroups(middlewareGroups)
	if err != nil {
		return nil, err
	}
	return &RouteGroup{
		router:      r,
		prefix:      prefix,
		middlewares: middlewares,
	}, nil
}

func (group *RouteGroup) Group(
	prefix string,
	middlewareGroups ...string,
) (*RouteGroup, error) {
	prefix, err := normalizeRoutePrefix(prefix)
	if err != nil {
		return nil, err
	}
	middlewares, err := group.router.resolveMiddlewareGroups(middlewareGroups)
	if err != nil {
		return nil, err
	}
	return &RouteGroup{
		router:      group.router,
		prefix:      joinRouteMethod(group.prefix, prefix),
		middlewares: appendCopy(group.middlewares, middlewares),
		policies:    append([]MethodPolicy(nil), group.policies...),
	}, nil
}

func (group *RouteGroup) Use(middlewares ...Middleware) {
	ensureMiddlewares(middlewares)
	group.middlewares = append(group.middlewares, middlewares...)
}

func (group *RouteGroup) UseMiddlewareGroups(names ...string) error {
	middlewares, err := group.router.resolveMiddlewareGroups(names)
	if err != nil {
		return err
	}
	group.middlewares = append(group.middlewares, middlewares...)
	return nil
}

func (group *RouteGroup) UsePolicies(policies ...MethodPolicy) {
	ensurePolicies(policies)
	group.policies = append(group.policies, policies...)
}

func (group *RouteGroup) Handle(method string, handler Handler) {
	group.router.register(
		joinRouteMethod(group.prefix, normalizeRouteMethod(method)),
		handler,
		group.middlewares,
		group.policies,
	)
}

func (group *RouteGroup) HandleWithPolicies(
	method string,
	handler Handler,
	policies ...MethodPolicy,
) {
	ensurePolicies(policies)
	group.router.register(
		joinRouteMethod(group.prefix, normalizeRouteMethod(method)),
		handler,
		group.middlewares,
		appendPolicies(group.policies, policies),
	)
}

func (r *Router) resolveMiddlewareGroups(names []string) ([]Middleware, error) {
	var resolved []Middleware
	for _, name := range names {
		name = strings.TrimSpace(name)
		middlewares, exists := r.middlewareGroups[name]
		if name == "" || !exists {
			return nil, fmt.Errorf("%w: %s", ErrMiddlewareGroupNotFound, name)
		}
		resolved = append(resolved, middlewares...)
	}
	return resolved, nil
}

func normalizeRoutePrefix(prefix string) (string, error) {
	prefix = strings.Trim(strings.TrimSpace(prefix), ".")
	if strings.Contains(prefix, "..") {
		return "", fmt.Errorf("%w: prefix %q", ErrInvalidRouteGroup, prefix)
	}
	return prefix, nil
}

func normalizeRouteMethod(method string) string {
	method = strings.TrimSpace(method)
	if method == "" || strings.HasPrefix(method, ".") || strings.HasSuffix(method, ".") || strings.Contains(method, "..") {
		panic(fmt.Sprintf("framework: invalid route method %q", method))
	}
	return method
}

func joinRouteMethod(prefix, method string) string {
	if prefix == "" {
		return method
	}
	if method == "" {
		return prefix
	}
	return prefix + "." + method
}

func validateMiddlewares(middlewares []Middleware) error {
	for _, middleware := range middlewares {
		if middleware == nil {
			return ErrInvalidMiddlewareGroup
		}
	}
	return nil
}

func ensureMiddlewares(middlewares []Middleware) {
	if err := validateMiddlewares(middlewares); err != nil {
		panic(err)
	}
}

func ensurePolicies(policies []MethodPolicy) {
	for _, policy := range policies {
		if policy == nil {
			panic("framework: method policy cannot be nil")
		}
	}
}

func appendCopy[T any](left, right []T) []T {
	combined := make([]T, 0, len(left)+len(right))
	combined = append(combined, left...)
	combined = append(combined, right...)
	return combined
}

func appendPolicies(left, right []MethodPolicy) []MethodPolicy {
	return appendCopy(left, right)
}
