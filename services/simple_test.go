package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateSecureSessionID(t *testing.T) {
	sessionID1 := GenerateSecureSessionID()
	sessionID2 := GenerateSecureSessionID()

	// Should generate different IDs
	assert.NotEqual(t, sessionID1, sessionID2)

	// Should not be empty
	assert.NotEmpty(t, sessionID1)
	assert.NotEmpty(t, sessionID2)

	// Should start with "session_"
	assert.Contains(t, sessionID1, "session_")
	assert.Contains(t, sessionID2, "session_")
}

func TestIsValidSessionID(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		expected  bool
	}{
		{"Valid session ID", "session_abc123-def456", true},
		{"Valid long session ID", "session_1234567890abcdef1234567890abcdef12345678", true},
		{"Empty session ID", "", false},
		{"Too short session ID", "short", false},
		{"Too long session ID", "this_is_a_very_long_session_id_that_exceeds_the_maximum_allowed_length_of_128_characters_and_should_be_rejected_by_validation_function_because_it_is_way_too_long_for_any_reasonable_session_id_format", false},
		{"Invalid characters", "session@123#456", false},
		{"Valid with underscores", "session_123_456", true},
		{"Valid with hyphens", "session-123-456", true},
		{"Mixed valid characters", "session_123-abc_DEF-789", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidSessionID(tt.sessionID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Test that the session ID validation is working properly
func TestSessionIDValidation_Integration(t *testing.T) {
	// Generate some session IDs and verify they all pass validation
	for i := 0; i < 100; i++ {
		sessionID := GenerateSecureSessionID()
		assert.True(t, isValidSessionID(sessionID), "Generated session ID should be valid: %s", sessionID)
	}
}

// Benchmark the session ID generation
func BenchmarkGenerateSecureSessionID(b *testing.B) {
	for i := 0; i < b.N; i++ {
		GenerateSecureSessionID()
	}
}

func BenchmarkIsValidSessionID(b *testing.B) {
	sessionID := "session_1234567890abcdef1234567890abcdef"
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isValidSessionID(sessionID)
	}
}