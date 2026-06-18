package livekit_test

import (
	"context"
	"errors"
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lkprotocol "github.com/livekit/protocol/livekit"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestSIPProtocolAliasesUseLiveKitTypes(t *testing.T) {
	req := &workerlivekit.SIPCreateParticipantRequest{RoomName: "room-a"}
	info := &workerlivekit.SIPParticipantInfo{ParticipantId: "caller-a"}

	if (*lkprotocol.CreateSIPParticipantRequest)(req).GetRoomName() != "room-a" {
		t.Fatal("CreateSIPParticipantRequest alias did not preserve room name")
	}
	if (*lkprotocol.SIPParticipantInfo)(info).GetParticipantId() != "caller-a" {
		t.Fatal("SIPParticipantInfo alias did not preserve participant id")
	}
}

func TestCreateSIPParticipantRequestUsesReferenceDefaultName(t *testing.T) {
	req := workerlivekit.CreateSIPParticipantRequest("room-a", "+15551234567", "trunk-a", "caller-a", "")

	if req.RoomName != "room-a" {
		t.Fatalf("CreateSIPParticipantRequest.RoomName = %q, want room-a", req.RoomName)
	}
	if req.ParticipantIdentity != "caller-a" {
		t.Fatalf("CreateSIPParticipantRequest.ParticipantIdentity = %q, want caller-a", req.ParticipantIdentity)
	}
	if req.SipTrunkId != "trunk-a" {
		t.Fatalf("CreateSIPParticipantRequest.SipTrunkId = %q, want trunk-a", req.SipTrunkId)
	}
	if req.SipCallTo != "+15551234567" {
		t.Fatalf("CreateSIPParticipantRequest.SipCallTo = %q, want +15551234567", req.SipCallTo)
	}
	if req.ParticipantName != "SIP-participant" {
		t.Fatalf("CreateSIPParticipantRequest.ParticipantName = %q, want SIP-participant", req.ParticipantName)
	}
}

func TestCreateSIPParticipantRequestPreservesExplicitName(t *testing.T) {
	req := workerlivekit.CreateSIPParticipantRequest("room-a", "+15551234567", "trunk-a", "caller-a", "SIP Caller")

	if req.ParticipantName != "SIP Caller" {
		t.Fatalf("CreateSIPParticipantRequest.ParticipantName = %q, want SIP Caller", req.ParticipantName)
	}
}

func TestJobCreateSIPParticipantRequestUsesJobRoomName(t *testing.T) {
	req := workerlivekit.JobCreateSIPParticipantRequest(&lkprotocol.Job{
		Room: &lkprotocol.Room{Name: "room-a"},
	}, "+15551234567", "trunk-a", "caller-a", "")

	if req.RoomName != "room-a" {
		t.Fatalf("JobCreateSIPParticipantRequest.RoomName = %q, want room-a", req.RoomName)
	}
	if req.ParticipantName != workerlivekit.DefaultSIPParticipantName {
		t.Fatalf("JobCreateSIPParticipantRequest.ParticipantName = %q, want default name", req.ParticipantName)
	}
}

func TestTransferSIPParticipantRequestMatchesReferenceFields(t *testing.T) {
	req := workerlivekit.TransferSIPParticipantRequest("room-a", "caller-a", "+15557654321", true)

	if req.RoomName != "room-a" {
		t.Fatalf("TransferSIPParticipantRequest.RoomName = %q, want room-a", req.RoomName)
	}
	if req.ParticipantIdentity != "caller-a" {
		t.Fatalf("TransferSIPParticipantRequest.ParticipantIdentity = %q, want caller-a", req.ParticipantIdentity)
	}
	if req.TransferTo != "+15557654321" {
		t.Fatalf("TransferSIPParticipantRequest.TransferTo = %q, want +15557654321", req.TransferTo)
	}
	if !req.PlayDialtone {
		t.Fatal("TransferSIPParticipantRequest.PlayDialtone = false, want true")
	}
}

func TestJobTransferSIPParticipantRequestUsesJobRoomName(t *testing.T) {
	req := workerlivekit.JobTransferSIPParticipantRequest(&lkprotocol.Job{
		Room: &lkprotocol.Room{Name: "room-a"},
	}, "caller-a", "+15557654321", false)

	if req.RoomName != "room-a" {
		t.Fatalf("JobTransferSIPParticipantRequest.RoomName = %q, want room-a", req.RoomName)
	}
	if req.ParticipantIdentity != "caller-a" {
		t.Fatalf("JobTransferSIPParticipantRequest.ParticipantIdentity = %q, want caller-a", req.ParticipantIdentity)
	}
	if req.PlayDialtone {
		t.Fatal("JobTransferSIPParticipantRequest.PlayDialtone = true, want false")
	}
}

func TestCreateSIPParticipantCallsSIPAPI(t *testing.T) {
	api := &fakeSIPAPI{}
	info, err := workerlivekit.CreateSIPParticipant(context.Background(), api, &lkprotocol.Job{
		Room: &lkprotocol.Room{Name: "room-a"},
	}, "+15551234567", "trunk-a", "caller-a", "")
	if err != nil {
		t.Fatalf("CreateSIPParticipant() error = %v", err)
	}
	if info == nil {
		t.Fatal("CreateSIPParticipant() info = nil")
	}
	if api.createRequest == nil {
		t.Fatal("CreateSIPParticipant() did not call SIP API")
	}
	if api.createRequest.RoomName != "room-a" {
		t.Fatalf("CreateSIPParticipant().RoomName = %q, want room-a", api.createRequest.RoomName)
	}
	if api.createRequest.ParticipantName != workerlivekit.DefaultSIPParticipantName {
		t.Fatalf("CreateSIPParticipant().ParticipantName = %q, want default", api.createRequest.ParticipantName)
	}
}

func TestCreateSIPParticipantWithNamesUsesFirstOptionalName(t *testing.T) {
	api := &fakeSIPAPI{}
	info, err := workerlivekit.CreateSIPParticipantWithNames(context.Background(), api, &lkprotocol.Job{
		Room: &lkprotocol.Room{Name: "room-a"},
	}, "+15551234567", "trunk-a", "caller-a", "SIP Caller", "ignored")
	if err != nil {
		t.Fatalf("CreateSIPParticipantWithNames() error = %v", err)
	}
	if info == nil {
		t.Fatal("CreateSIPParticipantWithNames() info = nil")
	}
	if api.createRequest == nil {
		t.Fatal("CreateSIPParticipantWithNames() did not call SIP API")
	}
	if api.createRequest.ParticipantName != "SIP Caller" {
		t.Fatalf("ParticipantName = %q, want SIP Caller", api.createRequest.ParticipantName)
	}
}

func TestCreateSIPParticipantWithRequestUsesProvidedRequest(t *testing.T) {
	api := &fakeSIPAPI{}
	req := &lkprotocol.CreateSIPParticipantRequest{RoomName: "room-a", ParticipantIdentity: "caller-a"}
	info, err := workerlivekit.CreateSIPParticipantWithRequest(context.Background(), api, req)
	if err != nil {
		t.Fatalf("CreateSIPParticipantWithRequest() error = %v", err)
	}
	if info == nil {
		t.Fatal("CreateSIPParticipantWithRequest() info = nil")
	}
	if api.createRequest != req {
		t.Fatalf("CreateSIPParticipantWithRequest() request = %#v, want provided request", api.createRequest)
	}
}

func TestTransferSIPParticipantCallsSIPAPI(t *testing.T) {
	api := &fakeSIPAPI{}
	err := workerlivekit.TransferSIPParticipant(context.Background(), api, &lkprotocol.Job{
		Room: &lkprotocol.Room{Name: "room-a"},
	}, "caller-a", "+15557654321", true)
	if err != nil {
		t.Fatalf("TransferSIPParticipant() error = %v", err)
	}
	if api.transferRequest == nil {
		t.Fatal("TransferSIPParticipant() did not call SIP API")
	}
	if api.transferRequest.RoomName != "room-a" {
		t.Fatalf("TransferSIPParticipant().RoomName = %q, want room-a", api.transferRequest.RoomName)
	}
	if !api.transferRequest.PlayDialtone {
		t.Fatal("TransferSIPParticipant().PlayDialtone = false, want true")
	}
}

func TestTransferSIPParticipantByParticipantDefaultsDialtone(t *testing.T) {
	api := &fakeSIPAPI{}

	err := workerlivekit.TransferSIPParticipantByParticipant(context.Background(), api, &lkprotocol.Job{
		Room: &lkprotocol.Room{Name: "room-a"},
	}, "caller-a", "+15557654321")
	if err != nil {
		t.Fatalf("TransferSIPParticipantByParticipant() error = %v", err)
	}
	if api.transferRequest == nil {
		t.Fatal("TransferSIPParticipantByParticipant() did not call SIP API")
	}
	if api.transferRequest.ParticipantIdentity != "caller-a" {
		t.Fatalf("ParticipantIdentity = %q, want caller-a", api.transferRequest.ParticipantIdentity)
	}
	if api.transferRequest.PlayDialtone {
		t.Fatal("PlayDialtone = true, want default false")
	}
}

func TestTransferSIPParticipantReturnsSIPAPIError(t *testing.T) {
	wantErr := errors.New("transfer failed")
	api := &fakeSIPAPI{err: wantErr}

	err := workerlivekit.TransferSIPParticipant(context.Background(), api, nil, "caller-a", "+15557654321", false)
	if !errors.Is(err, wantErr) {
		t.Fatalf("TransferSIPParticipant() error = %v, want %v", err, wantErr)
	}
}

type fakeSIPAPI struct {
	createRequest   *lkprotocol.CreateSIPParticipantRequest
	transferRequest *lkprotocol.TransferSIPParticipantRequest
	err             error
}

func (f *fakeSIPAPI) CreateSIPParticipant(_ context.Context, req *lkprotocol.CreateSIPParticipantRequest) (*lkprotocol.SIPParticipantInfo, error) {
	f.createRequest = req
	if f.err != nil {
		return nil, f.err
	}
	return &lkprotocol.SIPParticipantInfo{}, nil
}

func (f *fakeSIPAPI) TransferSIPParticipant(_ context.Context, req *lkprotocol.TransferSIPParticipantRequest) (*emptypb.Empty, error) {
	f.transferRequest = req
	if f.err != nil {
		return nil, f.err
	}
	return &emptypb.Empty{}, nil
}
