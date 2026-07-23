package events

import "github.com/cluion/bridra/backend/app/models"

type GreetingCreated struct {
	Greeting models.Greeting
}
