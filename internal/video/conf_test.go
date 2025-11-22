package video

import (
	"testing"
)

func TestValidateOutputType(t *testing.T) {
	tests := []struct {
		name    string
		input   OutputType
		wantErr bool
	}{
		{
			name:    "valid local",
			input:   LOCAL,
			wantErr: false,
		},
		{
			name:    "valid url",
			input:   URL,
			wantErr: false,
		},
		{
			name:    "valid unset",
			input:   UNSET,
			wantErr: false,
		},
		{
			name:    "invalid type",
			input:   "invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateOutputType(tt.input); (err != nil) != tt.wantErr {
				t.Errorf("ValidateOutputType() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
