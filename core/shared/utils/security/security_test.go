package security_test

import (
	"milton_prism/core/shared/utils/security"
	"testing"
)

func TestIsValidPassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		want     bool
	}{
		// Valid passwords
		{
			name:     "Valid password with all requirements",
			password: "SecurePass1!",
			want:     true,
		},
		{
			name:     "Valid password with minimum length",
			password: "Ab1!defg",
			want:     true,
		},
		{
			name:     "Valid password with maximum length",
			password: "Ab1!defgAb1!defgAb1!defgAb1!defgAb1!defgAb1!defgAb1!defgAb1!defg",
			want:     true,
		},
		{
			name:     "Valid password with all special chars",
			password: "aA1.2_3%4+5-6@7$8!9%0*1?2&3", // Now includes lowercase 'a'
			want:     true,
		},
		{
			name:     "Valid with exactly one of each type",
			password: "A1!bcdef", // A (upper), 1 (number), ! (special), b (lower)
			want:     true,
		},
		{
			name:     "Missing lowercase but has others",
			password: "A1!BCDEF", // No lowercase
			want:     false,
		},

		// Invalid passwords - length
		{
			name:     "Too short",
			password: "A1!def",
			want:     false,
		},
		{
			name:     "Too long",
			password: "Ab1!defgAb1!defgAb1!defgAb1!defgAb1!defgAb1!defgAb1!defgAb1!defgX",
			want:     false,
		},

		// Invalid passwords - missing requirements
		{
			name:     "Missing uppercase",
			password: "securepass1!",
			want:     false,
		},
		{
			name:     "Missing lowercase",
			password: "SECUREPASS1!",
			want:     false,
		},
		{
			name:     "Missing number",
			password: "SecurePass!",
			want:     false,
		},
		{
			name:     "Missing special char",
			password: "SecurePass1",
			want:     false,
		},

		// Invalid passwords - invalid characters
		{
			name:     "Contains space",
			password: "Secure Pass1!",
			want:     false,
		},
		{
			name:     "Contains unicode",
			password: "SécurePass1!",
			want:     false,
		},
		{
			name:     "Contains invalid special char",
			password: "SecurePass1#",
			want:     false,
		},

		// Edge cases
		{
			name:     "Empty string",
			password: "",
			want:     false,
		},
		{
			name:     "Whitespace only",
			password: "        ",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := security.IsValidPassword(tt.password, 8, 64); got != tt.want {
				t.Errorf("IsValidPassword() = %v, want %v (password: %q)", got, tt.want, tt.password)
			}
		})
	}
}
