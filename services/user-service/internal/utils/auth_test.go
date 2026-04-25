package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateTokens_Success(t *testing.T) {
	access, refresh, err := GenerateTokens(
		"42",
		"test@example.com",
		"CLIENT",
		[]string{"read", "write"},
		"access-secret-key",
		"refresh-secret-key",
	)
	require.NoError(t, err)
	assert.NotEmpty(t, access, "access token should not be empty")
	assert.NotEmpty(t, refresh, "refresh token should not be empty")
	assert.NotEqual(t, access, refresh, "access and refresh tokens should differ")
}

func TestGenerateTokens_EmptyPermissions(t *testing.T) {
	access, refresh, err := GenerateTokens(
		"1",
		"user@bank.rs",
		"EMPLOYEE",
		[]string{},
		"sec1",
		"sec2",
	)
	require.NoError(t, err)
	assert.NotEmpty(t, access)
	assert.NotEmpty(t, refresh)
}

func TestGenerateTokens_DifferentSecretsProduceDifferentTokens(t *testing.T) {
	a1, r1, err1 := GenerateTokens("1", "a@b.com", "CLIENT", nil, "secret-a", "refresh-a")
	a2, r2, err2 := GenerateTokens("1", "a@b.com", "CLIENT", nil, "secret-b", "refresh-b")
	require.NoError(t, err1)
	require.NoError(t, err2)
	assert.NotEqual(t, a1, a2, "different access secrets must produce different tokens")
	assert.NotEqual(t, r1, r2, "different refresh secrets must produce different tokens")
}

func TestGenerateTokens_DifferentUserIDs(t *testing.T) {
	a1, _, _ := GenerateTokens("1", "x@y.com", "CLIENT", nil, "sec", "rsec")
	a2, _, _ := GenerateTokens("2", "x@y.com", "CLIENT", nil, "sec", "rsec")
	assert.NotEqual(t, a1, a2, "different user IDs must produce different tokens")
}
