package bot

import (
	"context"
	"fmt"

	rpcclient "github.com/tendermint/tendermint/rpc/client"
	tmtypes "github.com/tendermint/tendermint/rpc/core/types"
	"go.uber.org/zap"
)

type eventer struct {
	rpc          rpcclient.Client
	eventClient  rpcclient.EventsClient
	subscriberID string
	logger       *zap.Logger
}

func newEventer(
	rpc rpcclient.Client,
	eventClient rpcclient.EventsClient,
	subscriberID string,
	logger *zap.Logger,
) *eventer {
	return &eventer{
		rpc:          rpc,
		eventClient:  eventClient,
		subscriberID: subscriberID,
		logger:       logger.With(zap.String("module", "eventer")),
	}
}

func (e *eventer) start() error {
	if err := e.rpc.Start(); err != nil {
		return fmt.Errorf("start rpc client: %w", err)
	}

	return nil
}

const (
	createdIROEvent = "dymensionxyz.dymension.eibc.EventNewIROPlan"
	settledIROEvent = "dymensionxyz.dymension.eibc.EventSettle"
)

func (e eventer) subscribeToCreatedIROs(ctx context.Context, callback func(ctx context.Context, name string, event tmtypes.ResultEvent) error) error {
	query := fmt.Sprintf("tm.event = '%s'", createdIROEvent)
	return e.subscribeToEvent(ctx, createdIROEvent, query, callback)
}

func (e eventer) subscribeToSettledIROs(ctx context.Context) error {
	query := fmt.Sprintf("tm.event = '%s'", settledIROEvent)
	return e.subscribeToEvent(ctx, settledIROEvent, query, func(ctx context.Context, name string, event tmtypes.ResultEvent) error {
		e.logger.Info("IRO plan settled", zap.Any("event", event))

		// Todo
		return nil
	})
}

func (e eventer) subscribeToEvent(ctx context.Context, event string, query string, callback func(ctx context.Context, name string, event tmtypes.ResultEvent) error) error {
	resCh, err := e.eventClient.Subscribe(ctx, e.subscriberID, query)
	if err != nil {
		return fmt.Errorf("failed to subscribe to %s events: %w", event, err)
	}

	go func() {
		for {
			select {
			case res := <-resCh:
				if err := callback(ctx, event, res); err != nil {
					e.logger.Error(fmt.Sprintf("failed to process %s event", event), zap.Error(err))
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return nil
}
