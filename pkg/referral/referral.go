// Package referral implements a two-sided referral program.
// Referrer and referee both receive a coupon (e.g., 1 month free).
package referral

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

var (
	// ErrCodeNotFound is returned when a referral code doesn't exist.
	ErrCodeNotFound = errors.New("referral: code not found")
	// ErrSelfReferral is returned when a user tries to use their own code.
	ErrSelfReferral = errors.New("referral: cannot use your own referral code")
	// ErrAlreadyReferred is returned when a tenant has already used a referral code.
	ErrAlreadyReferred = errors.New("referral: already used a referral code")
	// ErrCodeExpired is returned when a referral code has expired.
	ErrCodeExpired = errors.New("referral: code has expired")
)

// Referral represents a referral code and its usage.
type Referral struct {
	ID             string
	Code           string // 8-char hex code
	ReferrerTenant string
	RefereeTenant  string // empty if unused
	Status         string // active, used, expired
	CouponID       string // Stripe coupon ID applied
	CreatedAt      time.Time
	UsedAt         *time.Time
	ExpiresAt      time.Time
}

// ReferralRepo manages referral codes.
type ReferralRepo interface {
	Create(ctx context.Context, r *Referral) error
	GetByCode(ctx context.Context, code string) (*Referral, error)
	MarkUsed(ctx context.Context, code, refereeTenant, couponID string) error
	ListByTenant(ctx context.Context, tenantID string) ([]*Referral, error)
	CountByTenant(ctx context.Context, tenantID string) (int, error)
}

// GenerateCode creates a cryptographically random 8-character hex code.
func GenerateCode() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Service manages referral operations.
type Service struct {
	repo         ReferralRepo
	maxPerTenant int
	expiryDays   int
}

// NewService creates a referral service.
func NewService(repo ReferralRepo) *Service {
	return &Service{
		repo:         repo,
		maxPerTenant: 10,
		expiryDays:   90,
	}
}

// CreateCode generates a new referral code for a tenant.
func (s *Service) CreateCode(ctx context.Context, tenantID string) (*Referral, error) {
	count, err := s.repo.CountByTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if count >= s.maxPerTenant {
		return nil, errors.New("referral: maximum referral codes reached")
	}

	code, err := GenerateCode()
	if err != nil {
		return nil, err
	}

	r := &Referral{
		Code:           code,
		ReferrerTenant: tenantID,
		Status:         "active",
		CreatedAt:      time.Now().UTC(),
		ExpiresAt:      time.Now().UTC().AddDate(0, 0, s.expiryDays),
	}

	if err := s.repo.Create(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}

// RedeemCode applies a referral code for a new tenant.
func (s *Service) RedeemCode(ctx context.Context, code, refereeTenantID string) (*Referral, error) {
	r, err := s.repo.GetByCode(ctx, code)
	if err != nil {
		return nil, ErrCodeNotFound
	}

	if r.Status != "active" {
		return nil, ErrCodeExpired
	}
	if time.Now().After(r.ExpiresAt) {
		return nil, ErrCodeExpired
	}
	if r.ReferrerTenant == refereeTenantID {
		return nil, ErrSelfReferral
	}

	couponID := "coupon_" + code

	if err := s.repo.MarkUsed(ctx, code, refereeTenantID, couponID); err != nil {
		return nil, err
	}

	r.RefereeTenant = refereeTenantID
	r.Status = "used"
	r.CouponID = couponID
	now := time.Now().UTC()
	r.UsedAt = &now

	return r, nil
}
