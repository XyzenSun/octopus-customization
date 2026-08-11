package op

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/png"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const (
	// totpSkew 允许前后各 1 个 30 秒周期，即 ±30 秒。RFC 6238 建议值，
	totpSkew = 1
	// twoFactorMaxFailures 与 twoFactorLockDuration 控制爆破防护。
	twoFactorMaxFailures  = 5
	twoFactorLockDuration = 5 * time.Minute
)

// twoFactorGuard 是全局的 TOTP 失败计数器。单用户系统只有一个账号，
// 只统计"密码正确但 TOTP 错误"的失败：密码错误不计数
// 状态只存内存，重启清零
type twoFactorGuard struct {
	mu         sync.Mutex
	failures   int
	lockedTill time.Time
}

var totpGuard twoFactorGuard

// locked 返回当前是否处于锁定期，以及剩余时长。
func (g *twoFactorGuard) locked() (bool, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	remaining := time.Until(g.lockedTill)
	if remaining <= 0 {
		return false, 0
	}
	return true, remaining
}

func (g *twoFactorGuard) recordFailure() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failures++
	if g.failures >= twoFactorMaxFailures {
		g.lockedTill = time.Now().Add(twoFactorLockDuration)
		g.failures = 0
	}
}

func (g *twoFactorGuard) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failures = 0
	g.lockedTill = time.Time{}
}

// TwoFactorEnabled 返回两步验证是否已开启。设置项缺失时按关闭处理，
// 避免读取失败把用户挡在门外。
func TwoFactorEnabled() bool {
	enabled, err := SettingGetBool(model.SettingKeyTwoFactorEnabled)
	return err == nil && enabled
}

// TwoFactorSetup 生成新的 TOTP 密钥并落库，但不开启开关——必须经 TwoFactorEnable
// 验证一次验证码后才真正生效。这样扫码失败时最坏结果只是没绑成功，
// 不会出现"已开启却扫不出正确验证码"导致账号锁死（单用户系统没有第二个管理员能救）。
//
// 每次调用都覆盖旧密钥：重新点击绑定通常正是因为上一次流程出了问题，
// 沿用旧密钥只会把故障状态延续下去。
func TwoFactorSetup() (*model.TwoFactorSetupResponse, error) {
	if TwoFactorEnabled() {
		return nil, fmt.Errorf("two factor authentication is already enabled")
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      conf.APP_NAME,
		AccountName: userCache.Username,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate totp key: %w", err)
	}

	if err := userSetTwoFactorSecret(key.Secret()); err != nil {
		return nil, err
	}

	image, err := key.Image(256, 256)
	if err != nil {
		return nil, fmt.Errorf("failed to render qr code: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, image); err != nil {
		return nil, fmt.Errorf("failed to encode qr code: %w", err)
	}

	return &model.TwoFactorSetupResponse{
		Secret: key.Secret(),
		URI:    key.URL(),
		QRCode: "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()),
	}, nil
}

// TwoFactorEnable 校验验证码后开启两步验证。
func TwoFactorEnable(code string) error {
	if TwoFactorEnabled() {
		return fmt.Errorf("two factor authentication is already enabled")
	}
	if userCache.TwoFactorSecret == "" {
		return fmt.Errorf("two factor authentication is not set up")
	}
	if !totp.Validate(code, userCache.TwoFactorSecret) {
		return fmt.Errorf("invalid verification code")
	}
	return SettingSetString(model.SettingKeyTwoFactorEnabled, "true")
}

// TwoFactorDisable 校验当前验证码后关闭两步验证并清空密钥。
// 要求验证码而非密码：仅凭已签发的 JWT 就能一键关掉的话，这层防护形同虚设。
func TwoFactorDisable(code string) error {
	if !TwoFactorEnabled() {
		return fmt.Errorf("two factor authentication is not enabled")
	}
	if !verifyTOTPCode(code) {
		return fmt.Errorf("invalid verification code")
	}
	if err := SettingSetString(model.SettingKeyTwoFactorEnabled, "false"); err != nil {
		return err
	}
	if err := userSetTwoFactorSecret(""); err != nil {
		return err
	}
	totpGuard.reset()
	return nil
}

// TwoFactorForceDisable 供 CLI 使用：不校验验证码，直接关闭并清空密钥。
// 能执行 CLI 的人已经拥有服务器 shell，本就能直接改数据库，因此不引入新的攻击面。
func TwoFactorForceDisable() error {
	if err := SettingSetString(model.SettingKeyTwoFactorEnabled, "false"); err != nil {
		return err
	}
	return userSetTwoFactorSecret("")
}

// TwoFactorVerifyLogin 在登录时校验验证码，未开启两步验证时直接放行。
// 调用方必须已经确认密码正确——失败计数只针对 TOTP 环节。
func TwoFactorVerifyLogin(code string) error {
	if !TwoFactorEnabled() {
		return nil
	}
	if locked, remaining := totpGuard.locked(); locked {
		return fmt.Errorf("too many failed attempts, retry in %d seconds", int(remaining.Seconds())+1)
	}
	if !verifyTOTPCode(code) {
		totpGuard.recordFailure()
		return fmt.Errorf("invalid verification code")
	}
	totpGuard.reset()
	return nil
}

func verifyTOTPCode(code string) bool {
	if code == "" || userCache.TwoFactorSecret == "" {
		return false
	}
	valid, err := totp.ValidateCustom(code, userCache.TwoFactorSecret, time.Now(), totp.ValidateOpts{
		Period:    30,
		Skew:      totpSkew,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	return err == nil && valid
}

func userSetTwoFactorSecret(secret string) error {
	userCache.TwoFactorSecret = secret
	if err := db.GetDB().Model(&userCache).Update("two_factor_secret", secret).Error; err != nil {
		return fmt.Errorf("failed to update two factor secret: %w", err)
	}
	return nil
}
