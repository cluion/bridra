package framework

import (
	"context"
	"fmt"
)

type Handler func(*Context) (any, error)

type Middleware func(Handler) Handler

type MethodPolicy func(*Context) error

type routeDefinition struct {
	handler     Handler
	middlewares []Middleware
	policies    []MethodPolicy
}

type Router struct {
	routes           map[string]routeDefinition
	middlewares      []Middleware
	middlewareGroups map[string][]Middleware
	container        *Container
	exceptions       ExceptionRenderer
}

func NewRouter() *Router {
	return newRouter(nil)
}

func NewRouterWithContainer(container *Container) *Router {
	return newRouter(container)
}

func newRouter(container *Container) *Router {
	return &Router{
		routes:           make(map[string]routeDefinition),
		middlewareGroups: make(map[string][]Middleware),
		container:        container,
		exceptions:       DefaultExceptionRenderer(),
	}
}

func (r *Router) SetExceptionRenderer(renderer ExceptionRenderer) error {
	if exceptionRendererIsNil(renderer) {
		return ErrInvalidExceptionRenderer
	}
	r.exceptions = renderer
	return nil
}

func (r *Router) Use(middlewares ...Middleware) {
	ensureMiddlewares(middlewares)
	r.middlewares = append(r.middlewares, middlewares...)
}

func (r *Router) Handle(method string, handler Handler) {
	r.register(method, handler, nil, nil)
}

func (r *Router) HandleWithPolicies(
	method string,
	handler Handler,
	policies ...MethodPolicy,
) {
	r.register(method, handler, nil, policies)
}

func (r *Router) Dispatch(parent context.Context, request Request) Response {
	var scope *Scope
	if r.container != nil {
		scope = r.container.NewScope()
	}
	ctx := NewContextWithScope(parent, request, scope)
	route, exists := r.routes[request.Method]
	var handler Handler
	if !exists {
		handler = func(*Context) (any, error) {
			return nil, NewError("method_not_found", fmt.Sprintf("Unknown method %q.", request.Method))
		}
	} else {
		handler = applyPolicies(route.handler, route.policies)
		handler = applyMiddlewares(handler, route.middlewares)
	}

	handler = applyMiddlewares(handler, r.middlewares)

	result, err := handler(ctx)
	response := Response{
		ID: request.ID,
		Meta: map[string]any{
			"pipeline": ctx.Trace,
		},
	}
	if err != nil {
		response.Error = r.exceptions.Render(err)
		if response.Error == nil {
			response.Error = renderFrameworkException(err)
		}
		return response
	}
	response.Result = result
	return response
}

func (r *Router) register(
	method string,
	handler Handler,
	middlewares []Middleware,
	policies []MethodPolicy,
) {
	method = normalizeRouteMethod(method)
	if handler == nil {
		panic("framework: route handler cannot be nil")
	}
	ensureMiddlewares(middlewares)
	ensurePolicies(policies)
	if _, exists := r.routes[method]; exists {
		panic(fmt.Sprintf("framework: duplicate route %q", method))
	}
	r.routes[method] = routeDefinition{
		handler:     handler,
		middlewares: append([]Middleware(nil), middlewares...),
		policies:    append([]MethodPolicy(nil), policies...),
	}
}

func applyMiddlewares(handler Handler, middlewares []Middleware) Handler {
	for index := len(middlewares) - 1; index >= 0; index-- {
		handler = middlewares[index](handler)
	}
	return handler
}

func applyPolicies(handler Handler, policies []MethodPolicy) Handler {
	for index := len(policies) - 1; index >= 0; index-- {
		next := handler
		policy := policies[index]
		handler = func(ctx *Context) (any, error) {
			if err := policy(ctx); err != nil {
				return nil, err
			}
			return next(ctx)
		}
	}
	return handler
}
