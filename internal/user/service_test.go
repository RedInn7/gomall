package user

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/jwt"
)

func TestPasswordChangeEmailRequiresCurrentPassword(t *testing.T) {
	db := openUserTestDB(t)
	u := seedUserWithBalance(t, db, 1, 0)
	digest, err := bcrypt.GenerateFromPassword([]byte("old-pass"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(u).Updates(map[string]interface{}{"password_digest": string(digest), "email": "owner@example.com"}).Error; err != nil {
		t.Fatal(err)
	}
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 1})

	for _, current := range []string{"", "wrong-pass"} {
		_, err := GetUserSrv().SendEmail(ctx, &SendEmailServiceReq{
			OperationType: consts.EmailOperationUpdatePassword, Password: "new-pass",
			CurrentPassword: current, Email: "attacker@example.com",
		})
		if !errors.Is(err, ErrCurrentPasswordIncorrect) {
			t.Fatalf("current password %q: want ErrCurrentPasswordIncorrect, got %v", current, err)
		}
	}
}

func TestEmailTokenCannotBeReused(t *testing.T) {
	db := openUserTestDB(t)
	seedUserWithBalance(t, db, 1, 0)
	used := map[string]bool{}
	prevClaim, prevRelease := emailTokenClaim, emailTokenRelease
	emailTokenClaim = func(_ context.Context, key string, ttl time.Duration) (bool, error) {
		if ttl <= 0 {
			t.Fatalf("claim ttl must be positive, got %v", ttl)
		}
		if used[key] {
			return false, nil
		}
		used[key] = true
		return true, nil
	}
	emailTokenRelease = func(_ context.Context, key string) error {
		delete(used, key)
		return nil
	}
	t.Cleanup(func() { emailTokenClaim, emailTokenRelease = prevClaim, prevRelease })

	token, err := jwt.GenerateEmailToken(1, consts.EmailOperationUpdatePassword, "owner@example.com", "$2a$04$digest-from-mail")
	if err != nil {
		t.Fatal(err)
	}
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 1})
	if _, err := GetUserSrv().Valid(ctx, &ValidEmailServiceReq{Token: token}); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if _, err := GetUserSrv().Valid(ctx, &ValidEmailServiceReq{Token: token}); !errors.Is(err, ErrEmailTokenUsed) {
		t.Fatalf("second use: want ErrEmailTokenUsed, got %v", err)
	}

	var got User
	if err := db.First(&got, 1).Error; err != nil {
		t.Fatal(err)
	}
	if got.PasswordDigest != "$2a$04$digest-from-mail" || got.TokenVersion != 1 {
		t.Fatalf("password change must apply exactly once: digest=%q token_version=%d", got.PasswordDigest, got.TokenVersion)
	}
}
