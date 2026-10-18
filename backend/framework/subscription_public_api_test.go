package framework_test

import (
	"context"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

func TestPublicSubscriptionAPI(t *testing.T) {
	buffer, err := framework.NewSubscriptionBuffer[string](framework.SubscriptionBufferOptions{Capacity: 8})
	if err != nil {
		t.Fatal(err)
	}
	cursor := buffer.Cursor()
	if _, err = buffer.Publish("hello"); err != nil {
		t.Fatal(err)
	}
	buffer.Close()
	router := framework.NewRouter()
	router.Handle("logs.watch", func(ctx *framework.Context) (any, error) {
		return framework.ProduceSubscription(ctx, framework.SubscriptionOptions{AfterCursor: cursor}, buffer.Next)
	})
	count := 0
	if err = router.DispatchStream(context.Background(), framework.Request{ID: "1", Method: "logs.watch"}, func(response framework.Response) error { count++; return nil }); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("frames=%d", count)
	}
}
