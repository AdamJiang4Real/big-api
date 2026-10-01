package model

import (
	"errors"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const inviteCodeLength = 8

var (
	// ErrInviteCodeInvalid covers unknown, expired and already used codes alike
	// so registration responses do not reveal which case applied.
	ErrInviteCodeInvalid = errors.New("invite code is invalid or expired")
	// ErrInviteRecipientRegistered means the recipient already registered with
	// an invite code and gets no further codes.
	ErrInviteRecipientRegistered = errors.New("invite recipient already registered")
)

// InviteCode is a one-time registration code issued to an external recipient
// (for example a QQ user who asked the group bot) and consumed at sign-up.
type InviteCode struct {
	Id         int    `json:"id"`
	Code       string `json:"code" gorm:"type:varchar(32);uniqueIndex"`
	Source     string `json:"source" gorm:"type:varchar(32)"`
	IssuedTo   string `json:"issued_to" gorm:"type:varchar(64);index"`
	GroupId    string `json:"group_id" gorm:"type:varchar(64)"`
	CreatedAt  int64  `json:"created_at" gorm:"bigint"`
	ExpiresAt  int64  `json:"expires_at" gorm:"bigint;index"`
	UsedAt     int64  `json:"used_at" gorm:"bigint;default:0"`
	UsedUserId int    `json:"used_user_id" gorm:"default:0;index"`
}

// IssueInviteCode returns the recipient's pending code if one is still valid,
// otherwise a new code valid for ttl. Recipients who already registered with
// an invite code get ErrInviteRecipientRegistered.
func IssueInviteCode(source string, issuedTo string, groupId string, ttl time.Duration) (*InviteCode, error) {
	now := time.Now().Unix()
	var redeemed int64
	if err := DB.Model(&InviteCode{}).Where("issued_to = ? AND used_at > 0", issuedTo).Count(&redeemed).Error; err != nil {
		return nil, err
	}
	if redeemed > 0 {
		return nil, ErrInviteRecipientRegistered
	}

	var pending InviteCode
	err := DB.Where("issued_to = ? AND used_at = 0 AND expires_at > ?", issuedTo, now).Order("id desc").First(&pending).Error
	if err == nil {
		return &pending, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	code, err := common.GenerateRandomCharsKey(inviteCodeLength)
	if err != nil {
		return nil, err
	}
	invite := &InviteCode{
		// Upper case keeps lookups identical on case-insensitive MySQL
		// collations and case-sensitive PostgreSQL/SQLite comparisons.
		Code:      strings.ToUpper(code),
		Source:    source,
		IssuedTo:  issuedTo,
		GroupId:   groupId,
		CreatedAt: now,
		ExpiresAt: now + int64(ttl/time.Second),
	}
	if err := DB.Create(invite).Error; err != nil {
		return nil, err
	}
	return invite, nil
}

// ConsumeInviteCodeTx marks an unused, unexpired code as used by userId within
// tx. The conditional update makes concurrent registrations with the same
// code race-safe on every supported database: only one of them updates a row.
func ConsumeInviteCodeTx(tx *gorm.DB, code string, userId int) error {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return ErrInviteCodeInvalid
	}
	now := time.Now().Unix()
	result := tx.Model(&InviteCode{}).
		Where("code = ? AND used_at = 0 AND expires_at > ?", code, now).
		Updates(map[string]any{"used_at": now, "used_user_id": userId})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrInviteCodeInvalid
	}
	return nil
}
