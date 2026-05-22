package worker

import (
	"context"
	"fmt"

	tmgrpc "github.com/cavos-io/common-grpc/golang/telephony-manager"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// InboundRoutingResult holds the resolved routing from telephony-manager for an inbound SIP call.
type InboundRoutingResult struct {
	AgentId         uint64
	SipTrunkId      uint64
	InboundTrunkId  string
	DispatchRuleIds []string
	Message         string
}

// InboundRouter resolves inbound SIP call routing by calling the telephony-manager gRPC service.
type InboundRouter struct {
	client tmgrpc.SipTrunkServiceClient
	conn   *grpc.ClientConn
}

// NewInboundRouter creates an InboundRouter connected to the telephony-manager at addr.
func NewInboundRouter(addr string) (*InboundRouter, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("inbound router: dial telephony-manager at %s: %w", addr, err)
	}
	return &InboundRouter{
		client: tmgrpc.NewSipTrunkServiceClient(conn),
		conn:   conn,
	}, nil
}

// Resolve calls telephony-manager to fetch routing config for the given phone number ID.
func (r *InboundRouter) Resolve(ctx context.Context, phoneNumberId uint64, typ string) (*InboundRoutingResult, error) {
	resp, err := r.client.RefreshInboundDispatchByPhoneNumberId(ctx,
		&tmgrpc.RefreshInboundDispatchByPhoneNumberIdRequest{
			PhoneNumberId: phoneNumberId,
			Type:          typ,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("inbound router: resolve phone_number_id=%d: %w", phoneNumberId, err)
	}
	return &InboundRoutingResult{
		AgentId:         resp.GetAgentId(),
		SipTrunkId:      resp.GetSipTrunkId(),
		InboundTrunkId:  resp.GetInboundTrunkId(),
		DispatchRuleIds: resp.GetDispatchRuleIds(),
		Message:         resp.GetMessage(),
	}, nil
}

// Close releases the underlying gRPC connection.
func (r *InboundRouter) Close() error {
	return r.conn.Close()
}
