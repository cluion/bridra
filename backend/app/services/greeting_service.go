package services

import (
	"fmt"
	"strings"
	"time"

	"github.com/cluion/bridra/backend/app/models"
)

type GreetingService interface {
	Greet(name string) models.Greeting
}

type greetingService struct {
	now func() time.Time
}

func NewGreetingService() GreetingService {
	return &greetingService{now: time.Now}
}

func NewGreetingServiceWithClock(now func() time.Time) GreetingService {
	return &greetingService{now: now}
}

func (s *greetingService) Greet(name string) models.Greeting {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Flutter"
	}
	return models.Greeting{
		Message:   fmt.Sprintf("Hello, %s!", name),
		ServedBy:  "Go GreetingService",
		Timestamp: s.now(),
	}
}
