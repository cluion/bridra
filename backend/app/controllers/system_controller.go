package controllers

import (
	"github.com/cluion/bridra/backend/app/responses"
	"github.com/cluion/bridra/backend/framework"
)

type SystemController struct {
	runtime string
}

func NewSystemController(runtime ...string) *SystemController {
	name := "Go backend"
	if len(runtime) > 0 && runtime[0] != "" {
		name = runtime[0]
	}
	return &SystemController{runtime: name}
}

func (controller *SystemController) Health(*framework.Context) (any, error) {
	return responses.HealthResponse{
		Status:           "ok",
		FrameworkVersion: framework.FrameworkVersion,
		ProtocolVersion:  framework.ProtocolVersion,
		Runtime:          controller.runtime,
		Architecture:     "Middleware -> Controller -> Service",
	}, nil
}
