package framework_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

type publicOrderPlaced struct {
	Number string
}

func TestPublicTypedEventDispatcher(t *testing.T) {
	application := framework.NewApplication(nil)
	dispatcher, err := framework.Resolve(
		application.Container(),
		framework.EventDispatcherKey,
	)
	if err != nil {
		t.Fatalf("resolve dispatcher: %v", err)
	}
	seen := []string{}
	if err := framework.Listen(
		dispatcher,
		"public.audit",
		func(_ context.Context, event publicOrderPlaced) error {
			seen = append(seen, event.Number)
			return nil
		},
	); err != nil {
		t.Fatalf("listen: %v", err)
	}

	if err := framework.Dispatch(
		context.Background(),
		application.Events(),
		publicOrderPlaced{Number: "ORDER-1"},
	); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !reflect.DeepEqual(seen, []string{"ORDER-1"}) {
		t.Fatalf("seen = %#v", seen)
	}
	if names := framework.EventListeners[publicOrderPlaced](dispatcher); !reflect.DeepEqual(names, []string{"public.audit"}) {
		t.Fatalf("listeners = %#v", names)
	}
}
