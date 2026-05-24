package subscription

// CasdoorClient is the admin API surface the syncer needs.
type CasdoorClient interface {
	ListSubscriptions(org string) ([]Subscription, error)
	GetPlanRole(org, plan string) (string, error)
	UpdateUserProperties(org, user string, props UserProps) error
}

// Syncer mirrors subscription state into user properties so Casdoor emits them
// as JWT-Custom claims on the next token issuance/refresh.
type Syncer struct{ client CasdoorClient }

func NewSyncer(c CasdoorClient) *Syncer { return &Syncer{client: c} }

// SyncOrg pushes current subscription state into user properties for one org.
func (s *Syncer) SyncOrg(org string) error {
	subs, err := s.client.ListSubscriptions(org)
	if err != nil {
		return err
	}
	for _, sub := range subs {
		role, err := s.client.GetPlanRole(org, sub.Plan)
		if err != nil {
			return err
		}
		if err := s.client.UpdateUserProperties(org, sub.User, MapSubscriptionToUserProps(sub, role)); err != nil {
			return err
		}
	}
	return nil
}
