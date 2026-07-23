package framework_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/cluion/bridra/backend/framework"
)

type publicInvoicePaid struct {
	InvoiceID string
}

type publicSendReceipt struct {
	InvoiceID string
}

type publicQueuedEventProvider struct {
	receipts *[]string
}

func (provider *publicQueuedEventProvider) Register(application *framework.Application) error {
	queue, err := framework.Resolve(application.Container(), framework.JobQueueKey)
	if err != nil {
		return err
	}
	if err := framework.HandleJob(
		queue,
		"public.receipt",
		func(_ context.Context, job publicSendReceipt) error {
			*provider.receipts = append(*provider.receipts, job.InvoiceID)
			return nil
		},
	); err != nil {
		return err
	}
	return framework.ListenQueued(
		application.Events(),
		queue,
		"public.queue-receipt",
		func(_ context.Context, event publicInvoicePaid) (publicSendReceipt, error) {
			return publicSendReceipt{InvoiceID: event.InvoiceID}, nil
		},
	)
}

var _ framework.EventJobMapper[publicInvoicePaid, publicSendReceipt] = func(_ context.Context, event publicInvoicePaid) (publicSendReceipt, error) {
	return publicSendReceipt{InvoiceID: event.InvoiceID}, nil
}

func TestPublicQueuedEventListenerAPI(t *testing.T) {
	receipts := []string{}
	application := framework.NewApplication(nil)
	if err := application.Register(
		framework.NewQueueServiceProvider(framework.JobQueueOptions{Capacity: 2, Workers: 1}),
		&publicQueuedEventProvider{receipts: &receipts},
	); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := application.Boot(); err != nil {
		t.Fatalf("boot: %v", err)
	}
	if names := framework.EventListeners[publicInvoicePaid](application.Events()); !reflect.DeepEqual(names, []string{"public.queue-receipt"}) {
		t.Fatalf("listeners = %#v", names)
	}
	if err := framework.Dispatch(
		context.Background(),
		application.Events(),
		publicInvoicePaid{InvoiceID: "INVOICE-1"},
	); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if !reflect.DeepEqual(receipts, []string{"INVOICE-1"}) {
		t.Fatalf("receipts = %#v", receipts)
	}
}
