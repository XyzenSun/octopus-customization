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
