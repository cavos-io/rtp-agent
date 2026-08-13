package livekit

import (
	"testing"

	"github.com/go-jose/go-jose/v3/jwt"
	"github.com/livekit/protocol/auth"
)

func TestNewObservabilityTokenGrantsWrite(t *testing.T) {
	token, err := NewObservabilityToken("api-key", "api-secret")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := jwt.ParseSigned(token)
	if err != nil {
		t.Fatal(err)
	}
	grants := auth.ClaimGrants{}
	if err := parsed.Claims([]byte("api-secret"), &jwt.Claims{}, &grants); err != nil {
		t.Fatal(err)
	}
	if grants.Observability == nil || !grants.Observability.Write {
		t.Fatalf("observability grant = %#v, want write", grants.Observability)
	}
}
