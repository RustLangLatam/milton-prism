package grpc_client_sdk

import (
	"fmt"

	billingsvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/billing/v1"

	"google.golang.org/grpc"
)

// BillingGrpcClient is a gRPC client for the billing service. BillingService is
// served on the SAME endpoint as the analysis service, so this client is built
// over an existing analysis connection rather than dialing a new address.
type BillingGrpcClient struct {
	billingsvcv1.BillingServiceClient
	conn *grpc.ClientConn
}

// NewBillingGRPCClientOnConn builds a billing client over an existing gRPC
// connection (typically the analysis client's connection, since BillingService
// is co-served on the analysis-services endpoint). The connection lifecycle is
// owned by whoever created it — this client does not close it.
func NewBillingGRPCClientOnConn(conn *grpc.ClientConn) (*BillingGrpcClient, error) {
	if conn == nil {
		return nil, fmt.Errorf("billing grpc client: conn is nil")
	}
	return &BillingGrpcClient{
		BillingServiceClient: billingsvcv1.NewBillingServiceClient(conn),
		conn:                 conn,
	}, nil
}
