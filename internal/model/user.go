package model

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID              uint   `gorm:"primaryKey"`
	Username        string `gorm:"unique"`
	Password        string `gorm:"not null"`
	TwoFactorSecret string
}

type UserLogin struct {
	Username string `json:"username"`
	Password string `json:"password"`
	// TOTP 验证码
	Code string `json:"code"`
}

type UserChangePassword struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

type UserChangeUsername struct {
	NewUsername string `json:"new_username"`
}

// TwoFactorSetupResponse 绑定流程第一步的返回值。
// 同时给出二维码图片和 secret
type TwoFactorSetupResponse struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
	QRCode string `json:"qr_code"` // data:image/png;base64,... 可直接用作 <img src>
}

// TwoFactorCodeRequest 用于启用与关闭两步验证，两者都必须提供当前 TOTP 验证码。
type TwoFactorCodeRequest struct {
	Code string `json:"code"`
}

// ServerTimeResponse 供登录页在未登录状态下诊断时间偏差。
// TOTP 基于 Unix 时间戳，固定按东八区展示
type ServerTimeResponse struct {
	ServerTime       string `json:"server_time"`
	Timezone         string `json:"timezone"`
	TwoFactorEnabled bool   `json:"two_factor_enabled"`
}

func (u *User) HashPassword() error {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(u.Password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}
	u.Password = string(hashedPassword)
	return nil
}

func (u *User) ComparePassword(password string) error {
	return bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password))
}
