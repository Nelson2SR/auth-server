package main

import (
	"net/http"
	"os"

	"github.com/acme/auth-server/verifier"
)

func main() {
	pem, err := os.ReadFile(os.Getenv("CASDOOR_PUBLIC_KEY_PEM"))
	if err != nil {
		panic(err)
	}
	v, err := verifier.NewFromPEM(pem)
	if err != nil {
		panic(err)
	}

	http.HandleFunc("/pro-feature", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if len(auth) < 8 || auth[:7] != "Bearer " {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		claims, err := v.Authorize(auth[7:])
		if err == verifier.ErrInactiveSubscription {
			http.Error(w, "subscription required", http.StatusPaymentRequired)
			return
		}
		if err != nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		w.Write([]byte("welcome, plan=" + claims.Plan()))
	})

	http.ListenAndServe(":9000", nil)
}
