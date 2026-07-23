package controllers

import (
	"github.com/cluion/bridra/backend/app/events"
	"github.com/cluion/bridra/backend/app/requests"
	"github.com/cluion/bridra/backend/app/responses"
	"github.com/cluion/bridra/backend/app/services"
	"github.com/cluion/bridra/backend/framework"
)

type GreetingController struct {
	service    services.GreetingService
	dispatcher *framework.EventDispatcher
}

func NewGreetingController(service services.GreetingService) *GreetingController {
	return NewGreetingControllerWithEvents(service, framework.NewEventDispatcher())
}

func NewGreetingControllerWithEvents(
	service services.GreetingService,
	dispatcher *framework.EventDispatcher,
) *GreetingController {
	return &GreetingController{service: service, dispatcher: dispatcher}
}

func (controller *GreetingController) Hello(ctx *framework.Context) (any, error) {
	request, err := framework.BindAndValidate[requests.HelloRequest](ctx)
	if err != nil {
		return nil, err
	}
	greeting := controller.service.Greet(request.Name)
	if err := framework.Dispatch(ctx, controller.dispatcher, events.GreetingCreated{
		Greeting: greeting,
	}); err != nil {
		return nil, err
	}
	return responses.NewGreetingResponse(greeting), nil
}
