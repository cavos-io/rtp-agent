package main

import (
	"fmt"
	"os"
	"time"

	"github.com/livekit/protocol/auth"
)

func main() {
	apiKey := "APIbNwMFHLB4QtC"
	apiSecret := "ofPQ1UiLQ5Nf87lMX3pXrLyf87sBCz2iTMZ5eACocdoB"
	roomName := "test-room"
	identity := "user-test"

	if len(os.Args) > 1 {
		roomName = os.Args[1]
	}
	if len(os.Args) > 2 {
		identity = os.Args[2]
	}

	at := auth.NewAccessToken(apiKey, apiSecret)
	grant := &auth.VideoGrant{
		RoomJoin: true,
		Room:     roomName,
	}
	at.AddGrant(grant).
		SetIdentity(identity).
		SetValidFor(24 * time.Hour)

	token, err := at.ToJWT()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating token: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=== LiveKit Room Token ===")
	fmt.Printf("Room:     %s\n", roomName)
	fmt.Printf("Identity: %s\n", identity)
	fmt.Printf("URL:      wss://first-test-smn9006t.livekit.cloud\n")
	fmt.Printf("Token:    %s\n", token)
}
