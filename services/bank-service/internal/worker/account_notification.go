package worker

import (
	"encoding/json"
	"fmt"
	"log"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Tipovi account email evenata — koriste se kao vrednost polja Type.
const (
	AccountCreatedType       = "ACCOUNT_CREATED"
	CardStatusChangedType    = "CARD_STATUS_CHANGED"
	CardCreatedType          = "KREIRANA_KARTICA"
	KreditPodnetType         = "KREDIT_PODNET"
	KreditRataUpozorenjeType = "KREDIT_RATA_UPOZORENJE"
)

// AccountEmailEvent je payload za account-related emailove.
// Poklapa se sa core user-service EmailEvent contractom {type, email, token}
// pa notification-service može da ga konzumira bez izmene consumera.
type AccountEmailEvent struct {
	Type  string `json:"type"`  // "ACCOUNT_CREATED" | "CARD_STATUS_CHANGED"
	Email string `json:"email"` // email primaoca
	Token string `json:"token"` // novi status kartice za CARD_STATUS_CHANGED; prazno inače
}

// AccountEmailPublisher apstrahuje slanje account email notifikacija.
type AccountEmailPublisher interface {
	Publish(event AccountEmailEvent) error
}

// AMQPAccountPublisher publikuje AccountEmailEvent na email_notifications queue.
// Isti obrazac kao user-service AMQPPublisher: dial-per-publish, fire-and-forget.
type AMQPAccountPublisher struct {
	amqpURL string
}

// NewAMQPAccountPublisher kreira publisher vezan za dati RabbitMQ URL.
func NewAMQPAccountPublisher(amqpURL string) *AMQPAccountPublisher {
	return &AMQPAccountPublisher{amqpURL: amqpURL}
}

// Publish serijalizuje event u JSON i šalje ga na email_notifications queue.
func (p *AMQPAccountPublisher) Publish(event AccountEmailEvent) error {
	conn, err := amqp.Dial(p.amqpURL)
	if err != nil {
		return fmt.Errorf("amqp dial: %w", err)
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("amqp channel: %w", err)
	}
	defer ch.Close()

	_, err = ch.QueueDeclare(
		emailQueue,
		true,  // durable
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		nil,
	)
	if err != nil {
		return fmt.Errorf("queue declare: %w", err)
	}

	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("json marshal: %w", err)
	}

	return ch.Publish(
		"",
		emailQueue,
		false,
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         body,
		},
	)
}

// NoOpAccountEmailPublisher loguje event ali ništa ne šalje.
// Koristi se kada RABBITMQ_URL nije postavljen u env-u.
type NoOpAccountEmailPublisher struct{}

func (p *NoOpAccountEmailPublisher) Publish(event AccountEmailEvent) error {
	log.Printf("[worker/notif] NoOp — account email event type=%q to=%q nije poslat (RabbitMQ nije konfigurisan)",
		event.Type, event.Email)
	return nil
}
