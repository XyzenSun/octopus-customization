package model

import "testing"

func TestRelayLogMaxContentSizeValidate(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "unlimited", value: "-1", wantErr: false},
		{name: "zero", value: "0", wantErr: false},
		{name: "positive", value: "2", wantErr: false},
		{name: "less than unlimited sentinel", value: "-2", wantErr: true},
		{name: "not an integer", value: "1.5", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setting := Setting{Key: SettingKeyRelayLogMaxContentSizeMB, Value: tt.value}
			if err := setting.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func TestPassthroughEnabledDefaultAndValidate(t *testing.T) {
	defaultValue := ""
	for _, setting := range DefaultSettings() {
		if setting.Key == SettingKeyPassthroughEnabled {
			defaultValue = setting.Value
			break
		}
	}
	if defaultValue != "false" {
		t.Fatalf("passthrough default = %q, want false", defaultValue)
	}

	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "enabled", value: "true"},
		{name: "disabled", value: "false"},
		{name: "empty", value: "", wantErr: true},
		{name: "numeric boolean", value: "1", wantErr: true},
		{name: "mixed case", value: "True", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setting := Setting{Key: SettingKeyPassthroughEnabled, Value: tt.value}
			if err := setting.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func TestRelayLogMemoryLogMaxDimidiateTimesValidate(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "disable periodic gc", value: "-1", wantErr: false},
		{name: "minimum positive", value: "1", wantErr: false},
		{name: "default value", value: "15", wantErr: false},
		{name: "large value", value: "1000", wantErr: false},
		{name: "zero not allowed", value: "0", wantErr: true},
		{name: "negative below -1", value: "-2", wantErr: true},
		{name: "not an integer", value: "1.5", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setting := Setting{Key: SettingKeyRelayLogMemoryLogMaxDimidiateTimes, Value: tt.value}
			if err := setting.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}
