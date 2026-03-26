// sentiric-workflow-service/internal/client/clients.go
package client

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

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

	conns []*grpc.ClientConn
}

func NewClients(cfg *config.Config, log zerolog.Logger) (*GrpcClients, error) {
	log.Info().Str("event", "GRPC_CLIENTS_INIT").Msg("🔌 Workflow Service istemcileri (mTLS Destekli) hazırlanıyor...")

	var tlsCreds credentials.TransportCredentials
	var err error

	// [ARCH-COMPLIANCE] mtls_failure_policy: Sessiz güvensiz moda geçiş YASAKTIR.
	if cfg.CertPath == "" || cfg.KeyPath == "" || cfg.CaPath == "" {
		return nil, fmt.Errorf("FATAL [mTLS Policy Violation]: Sertifika yolları (CERT/KEY/CA) eksik belirtilmiş")
	}

	tlsCreds, err = loadClientTLS(cfg.CertPath, cfg.KeyPath, cfg.CaPath)
	if err != nil {
		return nil, fmt.Errorf("FATAL [mTLS Policy Violation]: Sertifikalar yüklenemedi: %w", err)
	}

	log.Info().Str("event", "MTLS_CERTS_LOADED").Msg("🔐 mTLS Sertifikaları başarıyla yüklendi.")

	// 1. Media Service
	mediaConn, err := connect(cfg.MediaServiceURL, "media-service", tlsCreds)
	if err != nil {
		return nil, fmt.Errorf("media service connect fail: %w", err)
	}

	// 2. Agent Service
	agentConn, err := connect(cfg.AgentServiceURL, "agent-service", tlsCreds)
	if err != nil {
		return nil, fmt.Errorf("agent service connect fail: %w", err)
	}

	// 3. B2BUA Service
	b2buaConn, err := connect(cfg.B2buaServiceURL, "b2bua-service", tlsCreds)
	if err != nil {
		return nil, fmt.Errorf("b2bua service connect fail: %w", err)
	}

	log.Info().Str("event", "GRPC_CLIENTS_READY").Msg("✅ Tüm gRPC istemcileri başarıyla bağlandı.")

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

func connect(targetURL string, serverName string, tlsCreds credentials.TransportCredentials) (*grpc.ClientConn, error) {
	var opts []grpc.DialOption

	cleanTarget := targetURL
	for _, prefix := range []string{"https://", "http://"} {
		cleanTarget = strings.TrimPrefix(cleanTarget, prefix)
	}

	// [ARCH-COMPLIANCE] Asla güvensiz credentials kullanılamaz.
	if tlsCreds == nil {
		return nil, fmt.Errorf("tlsCreds is nil, insecure fallback is forbidden")
	}

	opts = append(opts, grpc.WithTransportCredentials(tlsCreds))
	opts = append(opts, grpc.WithAuthority(serverName))

	return grpc.NewClient(cleanTarget, opts...)
}

func loadClientTLS(certPath, keyPath, caPath string) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("client cert load error: %w", err)
	}

	caPem, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("CA cert load error: %w", err)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caPem) {
		return nil, fmt.Errorf("failed to append CA cert")
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      certPool,
	}

	return credentials.NewTLS(tlsConfig), nil
}
