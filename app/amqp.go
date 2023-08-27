package app

import (
	"context"
	"fmt"
	amqp "github.com/rabbitmq/amqp091-go"
	"log"
	"time"
)

func (s *server) rabbit() {
	conn, err := amqp.Dial(fmt.Sprintf("amqp://%s:%s@%s:%s/",
		s.c.Amqp.User,
		s.c.Amqp.Pwd,
		s.c.Amqp.Host,
		s.c.Amqp.Port,
	))

	if err != nil {
		panic(err)
	}

	s.amqp = conn
}

func (s *server) Consume(queue string) {
	ch, _ := s.amqp.Channel()

	msgs, _ := ch.Consume(
		queue, // queue
		"",
		true,
		false,
		false,
		false,
		nil,
	)

	var forever chan struct{}

	go func() {
		for d := range msgs {
			log.Printf("Received a message: %s", d.Body)
		}
	}()

	log.Printf(" [*] Waiting for messages. To exit press CTRL+C")
	<-forever
}

func (s *server) produce(queue string, msg []byte) error {
	ch, _ := s.amqp.Channel()

	q, _ := ch.QueueDeclare(
		queue,
		false,
		false,
		false,
		false,
		nil,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return ch.PublishWithContext(ctx,
		"",
		q.Name,
		false,
		false,
		amqp.Publishing{
			ContentType: "text/plain",
			Body:        msg,
		})
}
