package referral

import (
	"context"
	"errors"
	"testing"
	"time"
)

type mockRepo struct {
	referrals map[string]*Referral
}

func newMockRepo() *mockRepo {
	return &mockRepo{referrals: make(map[string]*Referral)}
}

func (m *mockRepo) Create(_ context.Context, r *Referral) error {
	if r.ID == "" {
		r.ID = "ref-" + r.Code
	}
	m.referrals[r.Code] = r
	return nil
}

func (m *mockRepo) GetByCode(_ context.Context, code string) (*Referral, error) {
	r, ok := m.referrals[code]
	if !ok {
		return nil, errors.New("not found")
	}
	return r, nil
}

func (m *mockRepo) MarkUsed(_ context.Context, code, refereeTenant, couponID string) error {
	r, ok := m.referrals[code]
	if !ok {
		return errors.New("not found")
	}
	r.RefereeTenant = refereeTenant
	r.Status = "used"
	r.CouponID = couponID
	now := time.Now()
	r.UsedAt = &now
	return nil
}

func (m *mockRepo) ListByTenant(_ context.Context, tenantID string) ([]*Referral, error) {
	var result []*Referral
	for _, r := range m.referrals {
		if r.ReferrerTenant == tenantID {
			result = append(result, r)
		}
	}
	return result, nil
}

func (m *mockRepo) CountByTenant(_ context.Context, tenantID string) (int, error) {
	count := 0
	for _, r := range m.referrals {
		if r.ReferrerTenant == tenantID {
			count++
		}
	}
	return count, nil
}

func TestGenerateCode(t *testing.T) {
	code, err := GenerateCode()
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 8 {
		t.Errorf("code length = %d, want 8", len(code))
	}
	code2, _ := GenerateCode()
	if code == code2 {
		t.Error("codes should be unique")
	}
}

func TestCreateCode(t *testing.T) {
	svc := NewService(newMockRepo())
	r, err := svc.CreateCode(context.Background(), "tenant-1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Code == "" {
		t.Error("code should not be empty")
	}
	if r.ReferrerTenant != "tenant-1" {
		t.Errorf("referrer = %q, want tenant-1", r.ReferrerTenant)
	}
	if r.Status != "active" {
		t.Errorf("status = %q, want active", r.Status)
	}
}

func TestRedeemCode(t *testing.T) {
	svc := NewService(newMockRepo())
	ref, _ := svc.CreateCode(context.Background(), "tenant-1")

	redeemed, err := svc.RedeemCode(context.Background(), ref.Code, "tenant-2")
	if err != nil {
		t.Fatal(err)
	}
	if redeemed.RefereeTenant != "tenant-2" {
		t.Errorf("referee = %q, want tenant-2", redeemed.RefereeTenant)
	}
	if redeemed.Status != "used" {
		t.Errorf("status = %q, want used", redeemed.Status)
	}
	if redeemed.CouponID == "" {
		t.Error("coupon ID should not be empty")
	}
}

func TestRedeemCode_SelfReferral(t *testing.T) {
	svc := NewService(newMockRepo())
	ref, _ := svc.CreateCode(context.Background(), "tenant-1")
	_, err := svc.RedeemCode(context.Background(), ref.Code, "tenant-1")
	if !errors.Is(err, ErrSelfReferral) {
		t.Errorf("expected ErrSelfReferral, got %v", err)
	}
}

func TestRedeemCode_NotFound(t *testing.T) {
	svc := NewService(newMockRepo())
	_, err := svc.RedeemCode(context.Background(), "nonexistent", "tenant-1")
	if !errors.Is(err, ErrCodeNotFound) {
		t.Errorf("expected ErrCodeNotFound, got %v", err)
	}
}

func TestRedeemCode_Expired(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo)
	ref, _ := svc.CreateCode(context.Background(), "tenant-1")
	ref.ExpiresAt = time.Now().Add(-time.Hour)

	_, err := svc.RedeemCode(context.Background(), ref.Code, "tenant-2")
	if !errors.Is(err, ErrCodeExpired) {
		t.Errorf("expected ErrCodeExpired, got %v", err)
	}
}
