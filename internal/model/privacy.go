package model

import (
    "bytes"
    "database/sql"
    "database/sql/driver"
    "encoding/json"
    "fmt"
    "unicode/utf8"

    "github.com/bestruirui/octopus/internal/privacy"
    "gorm.io/gorm"
    "gorm.io/gorm/schema"
)

// PrivacyBytes 以不可变 string 持有业务值, SQL 绑定始终使用原始二进制字节.
// 构造不归一化或修复输入; 保存前仍须校验完整规则.
type PrivacyBytes struct {
    value string
}

func NewPrivacyBytes(value string) PrivacyBytes {
    return PrivacyBytes{value: value}
}

func (value PrivacyBytes) String() string {
    return value.value
}

func (value PrivacyBytes) MarshalJSON() ([]byte, error) {
    return privacy.EncodeJSONString(value.value)
}

func (value *PrivacyBytes) UnmarshalJSON(data []byte) error {
    decoded, err := privacy.DecodeJSONString(data)
    if err != nil {
        return privacy.ErrInvalidRule
    }
    value.value = decoded
    return nil
}

func (value PrivacyBytes) Value() (driver.Value, error) {
    // 每次返回独立切片, 不让驱动或调用方修改类型持有的值.
    return []byte(value.value), nil
}

func (value *PrivacyBytes) Scan(source any) error {
    data, ok := source.([]byte)
    if !ok {
        // 不接受 nil 或 TEXT/string, 防止绕过二进制比较语义.
        return privacy.ErrInvalidRule
    }
    value.value = string(data)
    return nil
}

func (PrivacyBytes) GormDataType() string {
    return "bytes"
}

func (PrivacyBytes) GormDBDataType(db *gorm.DB, field *schema.Field) string {
    switch db.Dialector.Name() {
    case "sqlite":
        return "BLOB"
    case "mysql":
        if field != nil && field.Size > 0 {
            return fmt.Sprintf("VARBINARY(%d)", field.Size)
        }
    case "postgres":
        return "BYTEA"
    }
    return ""
}

var (
    _ driver.Valuer = PrivacyBytes{}
    _ sql.Scanner   = (*PrivacyBytes)(nil)
)

type PrivacyRule struct {
    ID      int          `json:"id" gorm:"primaryKey;autoIncrement"`
    Source  PrivacyBytes `json:"source" gorm:"size:512;not null;uniqueIndex:ux_privacy_rules_source"`
    Alias   PrivacyBytes `json:"alias" gorm:"size:64;not null;uniqueIndex:ux_privacy_rules_alias"`
    Enabled bool         `json:"enabled" gorm:"not null;default:true"`
}

func (PrivacyRule) TableName() string {
    return "privacy_rules"
}

func (rule PrivacyRule) Rule() privacy.Rule {
    return privacy.Rule{
        ID:      rule.ID,
        Source:  rule.Source.String(),
        Alias:   rule.Alias.String(),
        Enabled: rule.Enabled,
    }
}

// Validate 校验业务字段; 新建候选的 ID 可为零, 更新 ID 由更新输入校验.
func (rule PrivacyRule) Validate() error {
    return privacy.ValidateRule(rule.Rule())
}

// 导入旧备份时缺省 enabled 与创建保持一致, 显式 null 则拒绝.
func (rule *PrivacyRule) UnmarshalJSON(data []byte) error {
    input, err := decodePrivacyRuleInput(data)
    if err != nil {
        return err
    }
    *rule = PrivacyRule{
        ID:      input.ID,
        Source:  input.Source,
        Alias:   input.Alias,
        Enabled: privacyRuleEnabled(input.Enabled),
    }
    return nil
}

type PrivacyRuleCreate struct {
    Source  PrivacyBytes `json:"source"`
    Alias   PrivacyBytes `json:"alias"`
    Enabled *bool        `json:"enabled,omitempty"`
}

func (input *PrivacyRuleCreate) UnmarshalJSON(data []byte) error {
    decoded, err := decodePrivacyRuleInput(data)
    if err != nil {
        return err
    }
    *input = PrivacyRuleCreate{
        Source:  decoded.Source,
        Alias:   decoded.Alias,
        Enabled: decoded.Enabled,
    }
    return nil
}

func (input PrivacyRuleCreate) Rule() privacy.Rule {
    return privacy.Rule{
        Source:  input.Source.String(),
        Alias:   input.Alias.String(),
        Enabled: privacyRuleEnabled(input.Enabled),
    }
}

func (input PrivacyRuleCreate) Validate() error {
    return privacy.ValidateRule(input.Rule())
}

type PrivacyRuleUpdate struct {
    ID      int          `json:"id"`
    Source  PrivacyBytes `json:"source"`
    Alias   PrivacyBytes `json:"alias"`
    Enabled *bool        `json:"enabled"`
}

func (input *PrivacyRuleUpdate) UnmarshalJSON(data []byte) error {
    decoded, err := decodePrivacyRuleInput(data)
    if err != nil {
        return err
    }
    *input = decoded
    return nil
}

// Rule 必须在 Validate 成功后使用; 更新不能省略 enabled.
func (input PrivacyRuleUpdate) Rule() privacy.Rule {
    return privacy.Rule{
        ID:      input.ID,
        Source:  input.Source.String(),
        Alias:   input.Alias.String(),
        Enabled: privacyRuleEnabled(input.Enabled),
    }
}

func (input PrivacyRuleUpdate) Validate() error {
    if input.ID <= 0 || input.Enabled == nil {
        return privacy.ErrInvalidRule
    }
    return privacy.ValidateRule(input.Rule())
}

func privacyRuleEnabled(enabled *bool) bool {
    return enabled == nil || *enabled
}

// RawMessage 保留 enabled 的缺字段/null 区别, 避免 *bool 的默认解码合并两者.
// 错误只返回固定类别, 不泄漏字段原文或标准库错误细节.
func decodePrivacyRuleInput(data []byte) (PrivacyRuleUpdate, error) {
    data = bytes.Trim(data, " \t\r\n")
    if len(data) == 0 || data[0] != '{' || !utf8.Valid(data) {
        return PrivacyRuleUpdate{}, privacy.ErrInvalidRule
    }
    var wire struct {
        ID      int             `json:"id"`
        Source  PrivacyBytes    `json:"source"`
        Alias   PrivacyBytes    `json:"alias"`
        Enabled json.RawMessage `json:"enabled"`
    }
    if err := json.Unmarshal(data, &wire); err != nil {
        return PrivacyRuleUpdate{}, privacy.ErrInvalidRule
    }
    input := PrivacyRuleUpdate{ID: wire.ID, Source: wire.Source, Alias: wire.Alias}
    if len(wire.Enabled) != 0 {
        raw := bytes.Trim(wire.Enabled, " \t\r\n")
        if !bytes.Equal(raw, []byte("true")) && !bytes.Equal(raw, []byte("false")) {
            return PrivacyRuleUpdate{}, privacy.ErrInvalidRule
        }
        enabled := bytes.Equal(raw, []byte("true"))
        input.Enabled = &enabled
    }
    return input, nil
}
