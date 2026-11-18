package main

import (
	"context"
	"encoding/json"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/smithy-go/tracing/smithyoteltracing"
	"go.opentelemetry.io/otel"
)

// SQSPublisher publica o evento "doação aprovada" na fila solidary-donations.
// Migrado do aws-sdk-go v1 (fim de suporte) para o v2, que tem suporte nativo a
// OpenTelemetry: a chamada ao SQS aparece como span no trace da doação.
// Credenciais: cadeia padrão da AWS (no EKS, a LabRole do nó via IMDS).
type SQSPublisher struct {
	client   *sqs.Client
	queueURL string
}

func NewSQSPublisher(ctx context.Context, region, queueURL string) (*SQSPublisher, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}
	client := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		o.TracerProvider = smithyoteltracing.Adapt(otel.GetTracerProvider())
	})
	return &SQSPublisher{client: client, queueURL: queueURL}, nil
}

func (p *SQSPublisher) Publish(ctx context.Context, d Donation) error {
	body, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, err = p.client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(p.queueURL),
		MessageBody: aws.String(string(body)),
	})
	return err
}
