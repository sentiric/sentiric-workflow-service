// sentiric-workflow-service/internal/client/clients.go
package client

import (
	"fmt"
	"strings"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	// Contracts
	agentv1 "github.com/sentiric/sentiric-contracts/gen/go/sentiric/agent/v1"
	mediav1 "github.com/sentiric/sentiric-contracts/gen/go/sentiric/media/v1"
	sipv1 "github.com/sentiric/sentiric-contracts/gen/go/sentiric/sip/v1"

	"github.com/sentiric/sentiric-workflow-service/internal/config"
)

type GrpcClients struct {
	Media mediav1.MediaServiceClient
	Agent agentv1.AgentOrchestrationServiceClient
	B2bua sipv1.B2BUAServiceClient

	// Bağlantıları kapatmak için
	conns []*grpc.ClientConn
}

func NewClients(cfg *config.Config, log zerolog.Logger) (*GrpcClients, error) {
	log.Info().Msg("🔌 Workflow Service istemcileri hazırlanıyor...")

	mediaConn, err := connect(cfg.MediaServiceURL)
	if err != nil {
		return nil, fmt.Errorf("media service connect fail: %w", err)
	}

	agentConn, err := connect(cfg.AgentServiceURL)
	if err != nil {
		return nil, fmt.Errorf("agent service connect fail: %w", err)
	}

	b2buaConn, err := connect(cfg.B2buaServiceURL)
	if err != nil {
		return nil, fmt.Errorf("b2bua service connect fail: %w", err)
	}

	return &GrpcClients{
		Media: mediav1.NewMediaServiceClient(mediaConn),
		Agent: agentv1.NewAgentOrchestrationServiceClient(agentConn),
		B2bua: sipv1.NewB2BUAServiceClient(b2buaConn),
		conns: []*grpc.ClientConn{mediaConn, agentConn, b2buaConn},
	}, nil
}

func (c *GrpcClients) Close() {
	for _, conn := range c.conns {
		conn.Close()
	}
}

func connect(targetURL string) (*grpc.ClientConn, error) {
	// Basitlik için şu an INSECURE (mTLS ileride eklenebilir)
	// URL temizleme
	clean := strings.TrimPrefix(strings.TrimPrefix(targetURL, "http://"), "https://")

	return grpc.NewClient(
		clean,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
}
