package model

import (
    "encoding/json"
    "time"

    "github.com/bestruirui/octopus/internal/privacy"
)

// DBDump is a full-database JSON export format for Octopus.
// Import uses incremental semantics (insert new rows, and upsert on certain key-based tables).
type DBDump struct {
	Version      int       `json:"version"`
	ExportedAt   time.Time `json:"exported_at"`
	IncludeLogs  bool      `json:"include_logs"`
	IncludeStats bool      `json:"include_stats"`

	Channels    []Channel    `json:"channels,omitempty"`
	ChannelKeys []ChannelKey `json:"channel_keys,omitempty"`
	Groups      []Group      `json:"groups,omitempty"`
	GroupItems []GroupItem `json:"group_items,omitempty"`
	LLMInfos   []LLMInfo   `json:"llm_infos,omitempty"`
	APIKeys    []APIKey    `json:"api_keys,omitempty"`
	Settings   []Setting   `json:"settings,omitempty"`
    PrivacyRules []PrivacyRule `json:"privacy_rules"`

	StatsTotal   []StatsTotal   `json:"stats_total,omitempty"`
	StatsDaily   []StatsDaily   `json:"stats_daily,omitempty"`
	StatsHourly  []StatsHourly  `json:"stats_hourly,omitempty"`
	StatsModel   []StatsModel   `json:"stats_model,omitempty"`
	StatsChannel []StatsChannel `json:"stats_channel,omitempty"`
	StatsAPIKey  []StatsAPIKey  `json:"stats_api_key,omitempty"`

	RelayLogs []RelayLog `json:"relay_logs,omitempty"`
}

// 备份中的隐私设置值也需严格校验. 仅用字符串字段会将 null 当作零值,
// 且类型错误可能泄露输入内容.
func (dump *DBDump) UnmarshalJSON(data []byte) error {
    type plainDump DBDump
    var decoded plainDump
    wire := struct {
        *plainDump
        Settings json.RawMessage `json:"settings"`
    }{plainDump: &decoded}
    if err := json.Unmarshal(data, &wire); err != nil {
        return err
    }
    if len(wire.Settings) != 0 {
        var settings []struct {
            Key   SettingKey      `json:"key"`
            Value json.RawMessage `json:"value"`
        }
        if err := json.Unmarshal(wire.Settings, &settings); err != nil {
            return err
        }
        for _, row := range settings {
            var value string
            if IsPrivacySettingKey(row.Key) {
                var err error
                value, err = privacy.DecodeJSONString(row.Value)
                if err != nil {
                    return privacy.ErrInvalidConfig
                }
            } else if len(row.Value) != 0 {
                if err := json.Unmarshal(row.Value, &value); err != nil {
                    return err
                }
            }
            decoded.Settings = append(decoded.Settings, Setting{Key: row.Key, Value: value})
        }
    }
    *dump = DBDump(decoded)
    return nil
}

type DBImportResult struct {
	// RowsAffected contains the rows affected for each table operation (insert/upsert depending on table).
	RowsAffected map[string]int64 `json:"rows_affected"`
}
