// Command genregtoken mints a one time registration token, the credential a
// new account needs to be created with:
//
//	go run ./cmd/admin/genregtoken -issued-by admin@example.com -ttl 24h
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"go-boilerplate/app"
	"go-boilerplate/pkg/auth"
)

func main() {
	issuedBy := flag.String("issued-by", "cli", "who the token is attributed to")
	ttl := flag.Duration("ttl", 0, "token lifetime, defaults to auth.registration_token_ttl")
	flag.Parse()

	cfg, err := app.LoadConfig()
	if err != nil {
		log.Fatalf("could not load config: %v", err)
	}

	db, err := app.OpenDB(cfg.DB)
	if err != nil {
		log.Fatalf("could not open database: %v", err)
	}
	defer app.CloseDB(db)

	service := auth.NewService(cfg.AuthConfig(), auth.NewGormStore(db), nil)

	raw, err := service.IssueRegistrationToken(context.Background(), auth.IssueRegistrationTokenInput{
		IssuedBy: *issuedBy,
		TTL:      *ttl,
	})
	if err != nil {
		log.Fatalf("could not issue registration token: %v", err)
	}

	fmt.Println(raw)
}
