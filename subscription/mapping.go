package subscription

import "time"

// Subscription is the subset of a Casdoor subscription we mirror into claims.
type Subscription struct {
	Owner   string
	Name    string
	User    string // user name within the org
	Plan    string
	State   string // Pending|Active|Upcoming|Suspended|Expired|Error
	EndDate time.Time
}

// UserProps are the Casdoor user properties emitted as JWT-Custom claims.
type UserProps map[string]string

// MapSubscriptionToUserProps converts a subscription (+ its plan's role) into
// the user properties Casdoor will emit as claims.
func MapSubscriptionToUserProps(s Subscription, role string) UserProps {
	return UserProps{
		"plan":                s.Plan,
		"role":                role,
		"subscriptionStatus":  s.State,
		"subscriptionEndTime": s.EndDate.UTC().Format(time.RFC3339),
	}
}
