package responses

import (
	"time"

	"github.com/cluion/bridra/backend/app/models"
)

func NewGreetingResponse(greeting models.Greeting) GreetingResponse {
	return GreetingResponse{
		Message:   greeting.Message,
		ServedBy:  greeting.ServedBy,
		Timestamp: greeting.Timestamp.UTC().Format(time.RFC3339),
	}
}
