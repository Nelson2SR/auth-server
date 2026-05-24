package main

import (
	"log"
	"os"

	"github.com/acme/auth-server/subscription"
)

// subsync mirrors current subscription state into user properties for one org,
// so Casdoor emits fresh subscription claims on the next token issuance.
// Env: CASDOOR_ENDPOINT, CASDOOR_CLIENT_ID, CASDOOR_CLIENT_SECRET, CASDOOR_ORG.
func main() {
	endpoint := os.Getenv("CASDOOR_ENDPOINT")
	cid := os.Getenv("CASDOOR_CLIENT_ID")
	secret := os.Getenv("CASDOOR_CLIENT_SECRET")
	org := os.Getenv("CASDOOR_ORG")
	if endpoint == "" || cid == "" || secret == "" || org == "" {
		log.Fatal("set CASDOOR_ENDPOINT, CASDOOR_CLIENT_ID, CASDOOR_CLIENT_SECRET, CASDOOR_ORG")
	}
	client := subscription.NewHTTPClient(endpoint, cid, secret)
	if err := subscription.NewSyncer(client).SyncOrg(org); err != nil {
		log.Fatalf("sync failed: %v", err)
	}
	log.Printf("synced subscriptions for org %s", org)
}
