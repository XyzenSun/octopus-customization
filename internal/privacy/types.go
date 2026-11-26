package privacy

import (
    "errors"
    "unicode/utf8"

    aho "github.com/petar-dambovaliev/aho-corasick"
)

type Scope uint8

const (
    Messages Scope = 1 << iota
    ToolDefinitions
    ToolCalls
    ToolResponses
    AllScopes = Messages | ToolDefinitions | ToolCalls | ToolResponses
)

type FailurePolicy string

const (
    Continue FailurePolicy = "continue"
    Abort    FailurePolicy = "abort"
)

const (
    MarkerLeft        = "[[OCTOPUS_PII:"
    MarkerRight       = "]]"
    MaxSourceBytes    = 512
    MaxAliasBytes     = 64
    MaxMarkerBytes    = len(MarkerLeft) + MaxAliasBytes + len(MarkerRight)
    MaxPendingEvents  = 1024
    MaxPendingBytes   = 8 * 1024 * 1024
    MaxLogicalStreams = 1024
    MaxJSONDepth      = 128
)

type ErrorKind string

const (
    ErrorCompile            ErrorKind = "compile"
    ErrorInvalidJSON        ErrorKind = "invalid_json"
    ErrorUnsupportedContent ErrorKind = "unsupported_content"
    ErrorMatch              ErrorKind = "match"
    ErrorStreamIdentity     ErrorKind = "stream_identity"
    ErrorResourceLimit      ErrorKind = "resource_limit"
)

// 错误只携带固定类别, 避免原文、数据库参数或 panic 内容进入响应和日志.
type PrivacyError struct {
    kind ErrorKind
}

func (privacyError *PrivacyError) Kind() ErrorKind {
    return privacyError.kind
}

func (privacyError *PrivacyError) Error() string {
    return "privacy processing failed: " + string(privacyError.kind)
}

var (
    ErrCompile            = &PrivacyError{kind: ErrorCompile}
    ErrInvalidJSON        = &PrivacyError{kind: ErrorInvalidJSON}
    ErrUnsupportedContent = &PrivacyError{kind: ErrorUnsupportedContent}
    ErrMatch              = &PrivacyError{kind: ErrorMatch}
    ErrStreamIdentity     = &PrivacyError{kind: ErrorStreamIdentity}
    ErrResourceLimit      = &PrivacyError{kind: ErrorResourceLimit}
    ErrInvalidRule        = errors.New("invalid privacy rule")
    ErrInvalidConfig      = errors.New("invalid privacy configuration")
    ErrRuleConflict       = errors.New("privacy rule conflict")
    ErrRuleNotFound       = errors.New("privacy rule not found")
)

type Rule struct {
    ID      int
    Source  string
    Alias   string
    Enabled bool
}

type Config struct {
    Enabled       bool
    Scopes        Scope
    FailurePolicy FailurePolicy
}

func DefaultConfig() Config {
    return Config{Scopes: Messages, FailurePolicy: Continue}
}

func ValidateConfig(config Config) error {
    if config.Scopes & ^AllScopes != 0 || (config.FailurePolicy != Continue && config.FailurePolicy != Abort) {
        return ErrInvalidConfig
    }
    return nil
}

func ValidateRule(rule Rule) error {
    if len(rule.Source) == 0 || len(rule.Source) > MaxSourceBytes || !utf8.ValidString(rule.Source) {
        return ErrInvalidRule
    }
    if len(rule.Alias) == 0 || len(rule.Alias) > MaxAliasBytes {
        return ErrInvalidRule
    }
    for index := 0; index < len(rule.Alias); index++ {
        if !isAliasByte(rule.Alias[index]) {
            return ErrInvalidRule
        }
    }
    return nil
}

func isAliasByte(value byte) bool {
    return value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_'
}

// 快照发布后只读. 所有容器和 matcher 都由组件私有持有, 请求仅保留快照引用.
type Snapshot struct {
    config          Config
    version         uint64
    rules           []Rule
    matcher         *aho.AhoCorasick
    patterns        []string
    markers         []string
    restoreMap      map[string]string
    maxMarkerLength int
}

func (snapshot *Snapshot) Config() Config {
    return snapshot.config
}

func (snapshot *Snapshot) Version() uint64 {
    return snapshot.version
}

// 单条值副本允许管理层构造候选集合, 不暴露快照内部的 slice 或 map.
func (snapshot *Snapshot) Rule(index int) (Rule, bool) {
    if index < 0 || index >= len(snapshot.rules) {
        return Rule{}, false
    }
    return snapshot.rules[index], true
}

func (snapshot *Snapshot) RuleCount() int {
    return len(snapshot.rules)
}

func (snapshot *Snapshot) Active() bool {
    return snapshot != nil && snapshot.config.Enabled && snapshot.config.Scopes != 0 && len(snapshot.patterns) > 0
}

func (snapshot *Snapshot) Processes(scope Scope) bool {
    return snapshot.Active() && snapshot.config.Scopes&scope != 0
}
