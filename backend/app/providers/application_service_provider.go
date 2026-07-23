package providers

import (
	"github.com/cluion/bridra/backend/app/contracts"
	"github.com/cluion/bridra/backend/app/controllers"
	"github.com/cluion/bridra/backend/app/services"
	"github.com/cluion/bridra/backend/app/settings"
	"github.com/cluion/bridra/backend/framework"
)

var GreetingServiceKey = framework.NewServiceKey[services.GreetingService]("greeting.service")

const rpcMiddlewareGroup = "rpc"

type ApplicationServiceProvider struct{}

func NewApplicationServiceProvider() ApplicationServiceProvider {
	return ApplicationServiceProvider{}
}

func (ApplicationServiceProvider) Register(application *framework.Application) error {
	return framework.Provide(
		application.Container(),
		GreetingServiceKey,
		func(*framework.Container) (services.GreetingService, error) {
			return services.NewGreetingService(), nil
		},
	)
}

func (ApplicationServiceProvider) Boot(application *framework.Application) error {
	greetingService, err := framework.Resolve(application.Container(), GreetingServiceKey)
	if err != nil {
		return err
	}
	eventDispatcher, err := framework.Resolve(
		application.Container(),
		framework.EventDispatcherKey,
	)
	if err != nil {
		return err
	}

	config := application.Config()
	greetingController := controllers.NewGreetingControllerWithEvents(
		greetingService,
		eventDispatcher,
	)
	systemController := controllers.NewSystemController(
		framework.ConfigValue(config, settings.RuntimeName),
	)

	router := application.Router()
	if err := router.RegisterMiddlewareGroup(
		rpcMiddlewareGroup,
		framework.Traced(
			"logging",
			framework.LogRequests(framework.ConfigValue(config, settings.LogOutput)),
		),
		framework.Traced("recovery", framework.Recovery()),
		framework.Traced("request-id", framework.RequireRequestID()),
		framework.Traced(
			"auth",
			framework.Authenticate(framework.ConfigValue(config, settings.BackendToken)),
		),
	); err != nil {
		return err
	}
	if err := router.UseMiddlewareGroups(rpcMiddlewareGroup); err != nil {
		return err
	}
	systemRoutes, err := router.Group(contracts.RouteGroupSystem)
	if err != nil {
		return err
	}
	greetingRoutes, err := router.Group(contracts.RouteGroupGreeting)
	if err != nil {
		return err
	}
	systemRoutes.Handle(contracts.RouteActionSystemHealth, systemController.Health)
	greetingRoutes.Handle(contracts.RouteActionGreetingHello, greetingController.Hello)
	return nil
}
