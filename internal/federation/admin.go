package federation

import (
	"context"
	"errors"
	"time"

	"github.com/keel-foundation/keel-core/internal/store"
)

type Snapshot struct {
	Domain         string                      `json:"domain"`
	BaseURL        string                      `json:"base_url"`
	ActorID        string                      `json:"actor_id"`
	ActorURL       string                      `json:"actor_url"`
	PolicyVersion  string                      `json:"policy_version"`
	PublicKeyPEM   string                      `json:"public_key_pem"`
	Peers          []store.FederationPeer      `json:"peers"`
	Followers      []store.FederationFollower  `json:"followers"`
	Following      []store.FederationFollowing `json:"following"`
	Deliveries     []store.FederationDelivery  `json:"deliveries"`
	QueuedDelivery int                         `json:"queued_deliveries"`
}

func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	if !s.Enabled() {
		return Snapshot{}, ErrDisabled
	}
	peers, err := s.store.ListFederationPeers(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	followers, err := s.store.ListFederationFollowers(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	following, err := s.store.ListFederationFollowing(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	deliveries, err := s.store.ListFederationDeliveries(ctx, 100)
	if err != nil {
		return Snapshot{}, err
	}
	queued := 0
	for _, delivery := range deliveries {
		if delivery.Status == "queued" || delivery.Status == "running" {
			queued++
		}
	}
	return Snapshot{
		Domain:         s.instance.Domain,
		BaseURL:        s.instance.BaseURL,
		ActorID:        s.instance.ActorID,
		ActorURL:       s.instanceActorURL(),
		PolicyVersion:  s.instance.PolicyVersion,
		PublicKeyPEM:   s.instance.PublicKeyPEM,
		Peers:          peers,
		Followers:      followers,
		Following:      following,
		Deliveries:     deliveries,
		QueuedDelivery: queued,
	}, nil
}

func (s *Service) SetPeerStatus(ctx context.Context, actorURL, status string) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	if status != "active" && status != "blocked" {
		return errors.New("status must be active or blocked")
	}
	return s.store.SetFederationPeerStatus(ctx, actorURL, status, s.now())
}

func (s *Service) PeerStatus(ctx context.Context, actorURL string) (string, error) {
	peer, err := s.store.GetFederationPeer(ctx, actorURL)
	if err != nil {
		return "", err
	}
	return peer.Status, nil
}

func (s *Service) ProcessDeliveriesNow(ctx context.Context) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	s.ProcessDeliveries(ctx, 50)
	return nil
}

func (s *Service) NextDeliveryAt(ctx context.Context) (time.Time, error) {
	if !s.Enabled() {
		return time.Time{}, ErrDisabled
	}
	deliveries, err := s.store.ListFederationDeliveries(ctx, 200)
	if err != nil {
		return time.Time{}, err
	}
	var next time.Time
	for _, delivery := range deliveries {
		if delivery.Status != "queued" {
			continue
		}
		if next.IsZero() || delivery.NextAttemptAt.Before(next) {
			next = delivery.NextAttemptAt
		}
	}
	return next, nil
}
