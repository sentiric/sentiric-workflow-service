package queue

import (
	"context"
	"sync"
	"time"

	"github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog"
)

const ghostBufSize = 1000

type GhostMessage struct {
	RoutingKey  string
	ContentType string
	Body        []byte
}

type RabbitMQ struct {
	url               string
	log               zerolog.Logger
	conn              *amqp091.Connection
	ch                *amqp091.Channel
	mu                sync.RWMutex
	buffer            []GhostMessage
	bufMu             sync.Mutex
	exchange          string
	topologySetupFunc func(ch *amqp091.Channel) error
	consumerFunc      func(ctx context.Context, ch *amqp091.Channel, wg *sync.WaitGroup)
}

func NewRabbitMQ(url string, log zerolog.Logger) *RabbitMQ {
	return &RabbitMQ{
		url:      url,
		log:      log,
		buffer:   make([]GhostMessage, 0, ghostBufSize),
		exchange: "sentiric_events",
	}
}

func (m *RabbitMQ) SetTopologyAndConsumer(setup func(*amqp091.Channel) error, cons func(context.Context, *amqp091.Channel, *sync.WaitGroup)) {
	m.topologySetupFunc = setup
	m.consumerFunc = cons
}

func (m *RabbitMQ) PublishProtobuf(ctx context.Context, routingKey string, body []byte) error {
	m.mu.RLock()
	ch := m.ch
	m.mu.RUnlock()

	if ch != nil && !ch.IsClosed() {
		err := ch.PublishWithContext(ctx, m.exchange, routingKey, false, false, amqp091.Publishing{
			ContentType:  "application/protobuf",
			Body:         body,
			DeliveryMode: amqp091.Persistent,
		})
		if err == nil {
			return nil
		}
	}

	m.log.Warn().Str("event", "RMQ_GHOST_MODE").Str("routing_key", routingKey).Msg("RabbitMQ çevrimdışı. Mesaj RAM tamponuna alınıyor.")

	m.bufMu.Lock()
	defer m.bufMu.Unlock()
	if len(m.buffer) >= ghostBufSize {
		m.buffer = m.buffer[1:] // FIFO Drop
	}
	m.buffer = append(m.buffer, GhostMessage{
		RoutingKey:  routingKey,
		ContentType: "application/protobuf",
		Body:        body,
	})
	return nil
}

func (m *RabbitMQ) flushBuffer(ctx context.Context) {
	m.bufMu.Lock()
	defer m.bufMu.Unlock()
	if len(m.buffer) == 0 {
		return
	}
	m.mu.RLock()
	ch := m.ch
	m.mu.RUnlock()
	if ch == nil || ch.IsClosed() {
		return
	}
	successCount := 0
	for _, msg := range m.buffer {
		err := ch.PublishWithContext(ctx, m.exchange, msg.RoutingKey, false, false, amqp091.Publishing{
			ContentType:  msg.ContentType,
			Body:         msg.Body,
			DeliveryMode: amqp091.Persistent,
		})
		if err != nil {
			break
		}
		successCount++
	}
	if successCount > 0 {
		m.buffer = m.buffer[successCount:]
		m.log.Info().Str("event", "RMQ_GHOST_FLUSH").Int("count", successCount).Msg("Ghost Buffer'daki mesajlar başarıyla RabbitMQ'ya aktarıldı.")
	}
}

func (m *RabbitMQ) Start(ctx context.Context, wg *sync.WaitGroup) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn, err := amqp091.Dial(m.url)
		if err != nil {
			m.log.Warn().Str("event", "RMQ_RECONNECT_WAIT").Err(err).Msg("RabbitMQ bağlantısı koptu, 5 saniye sonra tekrar denenecek...")
			time.Sleep(5 * time.Second)
			continue
		}
		ch, err := conn.Channel()
		if err != nil {
			conn.Close()
			time.Sleep(5 * time.Second)
			continue
		}

		m.mu.Lock()
		m.conn = conn
		m.ch = ch
		m.mu.Unlock()

		m.log.Info().Str("event", "RMQ_CONNECTED").Msg("✅ RabbitMQ bağlantısı sağlandı.")

		if m.topologySetupFunc != nil {
			if err := m.topologySetupFunc(ch); err != nil {
				m.log.Error().Str("event", "RMQ_TOPOLOGY_FAIL").Err(err).Msg("Topoloji kurulamadı")
				conn.Close()
				time.Sleep(5 * time.Second)
				continue
			}
		}

		m.flushBuffer(ctx)

		if m.consumerFunc != nil {
			m.consumerFunc(ctx, ch, wg)
		}

		m.mu.Lock()
		m.conn = nil
		m.ch = nil
		m.mu.Unlock()
	}
}
