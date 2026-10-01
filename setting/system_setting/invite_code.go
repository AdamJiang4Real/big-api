package system_setting

import (
	"time"

	"github.com/QuantumNous/new-api/setting/config"
)

// InviteCodeSettings controls invite-only registration. Codes are issued by a
// trusted bot (for example a QQ group bot) through the bot secret and are
// consumed once at registration. DB keys: invite_code.register_enabled,
// invite_code.expire_minutes, invite_code.bot_secret, invite_code.register_hint.
type InviteCodeSettings struct {
	RegisterEnabled bool   `json:"register_enabled"`
	ExpireMinutes   int    `json:"expire_minutes"`
	BotSecret       string `json:"bot_secret"`
	RegisterHint    string `json:"register_hint"`
}

var inviteCodeSettings = InviteCodeSettings{ExpireMinutes: 30}

func init() {
	config.GlobalConfig.Register("invite_code", &inviteCodeSettings)
}

func GetInviteCodeSettings() *InviteCodeSettings {
	return &inviteCodeSettings
}

// InviteCodeTTL is how long an issued invite code stays usable.
func (s *InviteCodeSettings) InviteCodeTTL() time.Duration {
	return time.Duration(max(s.ExpireMinutes, 1)) * time.Minute
}
