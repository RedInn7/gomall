package rabbitmq

import (
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/sirupsen/logrus"

	util "github.com/RedInn7/gomall/pkg/utils/log"
)

func TestDeliverWithRecoverRoutesPanicToConfiguredRetry(t *testing.T) {
	previousLogger := util.LogrusObj
	util.LogrusObj = logrus.New()
	t.Cleanup(func() { util.LogrusObj = previousLogger })
	delivery := amqp.Delivery{RoutingKey: "product.changed", Body: []byte(`{"product_id":1}`)}
	var recovered amqp.Delivery
	deliverWithRecover("search.product.vector-indexer", func(amqp.Delivery) {
		panic("temporary dependency failure")
	}, func(got amqp.Delivery) {
		recovered = got
	}, delivery)

	if recovered.RoutingKey != delivery.RoutingKey || string(recovered.Body) != string(delivery.Body) {
		t.Fatalf("panic delivery did not reach retry callback: %#v", recovered)
	}
}
