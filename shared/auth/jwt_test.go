package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testAccessSecret     = "test-access-secret-32-chars-long!"
	testRefreshSecret    = "test-refresh-secret-32-chars-lon!"
	testActivationSecret = "test-activation-secret-32chars!!"
)

// ─── GenerateTokens ───────────────────────────────────────────────────────────

func TestGenerateTokens(t *testing.T) {
	access, refresh, err := GenerateTokens("42", "user@test.com", "EMPLOYEE",
		[]string{"VIEW_ACCOUNTS"}, testAccessSecret, testRefreshSecret)

	require.NoError(t, err)
	assert.NotEmpty(t, access)
	assert.NotEmpty(t, refresh)
	assert.NotEqual(t, access, refresh)
}

func TestGenerateTokens_ClaimsRoundTrip(t *testing.T) {
	access, refresh, err := GenerateTokens("99", "u@x.com", "ADMIN",
		[]string{"ADMIN_PERMISSION"}, testAccessSecret, testRefreshSecret)
	require.NoError(t, err)

	// Verify access token claims
	claims, err := VerifyToken(access, testAccessSecret)
	require.NoError(t, err)
	assert.Equal(t, "99", claims.Subject)
	assert.Equal(t, "u@x.com", claims.Email)
	assert.Equal(t, "ADMIN", claims.UserType)
	assert.Equal(t, []string{"ADMIN_PERMISSION"}, claims.Permissions)

	// Verify refresh token claims
	rClaims, err := VerifyRefreshToken(refresh, testRefreshSecret)
	require.NoError(t, err)
	assert.Equal(t, "99", rClaims.Subject)
	assert.Equal(t, "refresh", rClaims.TokenType)
}

// ─── VerifyToken ──────────────────────────────────────────────────────────────

func TestVerifyToken(t *testing.T) {
	tests := []struct {
		name      string
		setup     func() string
		secret    string
		wantErr   bool
		errSubstr string
	}{
		{
			name: "valid access token",
			setup: func() string {
				t, _, _ := GenerateTokens("1", "a@b.com", "EMPLOYEE", nil, testAccessSecret, testRefreshSecret)
				return t
			},
			secret:  testAccessSecret,
			wantErr: false,
		},
		{
			name: "wrong secret",
			setup: func() string {
				t, _, _ := GenerateTokens("1", "a@b.com", "EMPLOYEE", nil, testAccessSecret, testRefreshSecret)
				return t
			},
			secret:  "wrong-secret",
			wantErr: true,
		},
		{
			name: "refresh token rejected as access token",
			setup: func() string {
				_, r, _ := GenerateTokens("1", "a@b.com", "EMPLOYEE", nil, testAccessSecret, testRefreshSecret)
				return r
			},
			secret:    testRefreshSecret,
			wantErr:   true,
			errSubstr: "refresh token cannot be used",
		},
		{
			name: "malformed token",
			setup: func() string {
				return "not.a.valid.token"
			},
			secret:  testAccessSecret,
			wantErr: true,
		},
		{
			name: "expired token",
			setup: func() string {
				claims := &AccessClaims{
					Email:    "a@b.com",
					UserType: "EMPLOYEE",
					RegisteredClaims: jwt.RegisteredClaims{
						Subject:   "1",
						IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
						ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
					},
				}
				tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testAccessSecret))
				return tok
			},
			secret:  testAccessSecret,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			token := tc.setup()
			claims, err := VerifyToken(token, tc.secret)
			if tc.wantErr {
				assert.Error(t, err)
				if tc.errSubstr != "" {
					assert.True(t, strings.Contains(err.Error(), tc.errSubstr),
						"expected error to contain %q, got %q", tc.errSubstr, err.Error())
				}
				assert.Nil(t, claims)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, claims)
			}
		})
	}
}

// ─── VerifyRefreshToken ───────────────────────────────────────────────────────

func TestVerifyRefreshToken(t *testing.T) {
	tests := []struct {
		name    string
		setup   func() string
		secret  string
		wantErr bool
	}{
		{
			name: "valid refresh token",
			setup: func() string {
				_, r, _ := GenerateTokens("1", "a@b.com", "EMPLOYEE", nil, testAccessSecret, testRefreshSecret)
				return r
			},
			secret:  testRefreshSecret,
			wantErr: false,
		},
		{
			name: "access token rejected as refresh",
			setup: func() string {
				a, _, _ := GenerateTokens("1", "a@b.com", "EMPLOYEE", nil, testAccessSecret, testRefreshSecret)
				return a
			},
			secret:  testAccessSecret,
			wantErr: true,
		},
		{
			name: "wrong secret",
			setup: func() string {
				_, r, _ := GenerateTokens("1", "a@b.com", "EMPLOYEE", nil, testAccessSecret, testRefreshSecret)
				return r
			},
			secret:  "wrong",
			wantErr: true,
		},
		{
			name: "expired refresh token",
			setup: func() string {
				claims := &RefreshClaims{
					TokenType: "refresh",
					RegisteredClaims: jwt.RegisteredClaims{
						Subject:   "1",
						IssuedAt:  jwt.NewNumericDate(time.Now().Add(-8 * 24 * time.Hour)),
						ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
					},
				}
				tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testRefreshSecret))
				return tok
			},
			secret:  testRefreshSecret,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			claims, err := VerifyRefreshToken(tc.setup(), tc.secret)
			if tc.wantErr {
				assert.Error(t, err)
				assert.Nil(t, claims)
			} else {
				require.NoError(t, err)
				require.NotNil(t, claims)
				assert.Equal(t, "refresh", claims.TokenType)
			}
		})
	}
}

// ─── Activation / Reset tokens ────────────────────────────────────────────────

func TestActivationToken_RoundTrip(t *testing.T) {
	tok, err := GenerateActivationToken("activate@test.com", testActivationSecret)
	require.NoError(t, err)
	require.NotEmpty(t, tok)

	email, err := VerifyActivationToken(tok, testActivationSecret)
	require.NoError(t, err)
	assert.Equal(t, "activate@test.com", email)
}

func TestVerifyActivationToken_WrongType(t *testing.T) {
	// A reset token must not verify as an activation token
	resetTok, _ := GenerateResetToken("x@y.com", testActivationSecret)
	_, err := VerifyActivationToken(resetTok, testActivationSecret)
	assert.Error(t, err)
}

func TestVerifyActivationToken_WrongSecret(t *testing.T) {
	tok, _ := GenerateActivationToken("x@y.com", testActivationSecret)
	_, err := VerifyActivationToken(tok, "wrong-secret")
	assert.Error(t, err)
}

func TestResetToken_RoundTrip(t *testing.T) {
	tok, err := GenerateResetToken("reset@test.com", testActivationSecret)
	require.NoError(t, err)
	require.NotEmpty(t, tok)

	email, err := VerifyResetToken(tok, testActivationSecret)
	require.NoError(t, err)
	assert.Equal(t, "reset@test.com", email)
}

func TestVerifyResetToken_WrongType(t *testing.T) {
	// An activation token must not verify as a reset token
	actTok, _ := GenerateActivationToken("x@y.com", testActivationSecret)
	_, err := VerifyResetToken(actTok, testActivationSecret)
	assert.Error(t, err)
}

func TestGenerateAccessToken_ClaimsRoundTrip(t *testing.T) {
	tok, err := GenerateAccessToken("5", "emp@test.com", "EMPLOYEE",
		[]string{"VIEW_ACCOUNTS", "MANAGE_ACCOUNTS"}, testAccessSecret)
	require.NoError(t, err)

	claims, err := VerifyToken(tok, testAccessSecret)
	require.NoError(t, err)
	assert.Equal(t, "5", claims.Subject)
	assert.Equal(t, "emp@test.com", claims.Email)
	assert.Equal(t, "EMPLOYEE", claims.UserType)
	assert.ElementsMatch(t, []string{"VIEW_ACCOUNTS", "MANAGE_ACCOUNTS"}, claims.Permissions)
}

func TestVerifyToken_UnexpectedSigningMethod(t *testing.T) {
	// Build a token signed with RS256 (not HS256) to trigger the signing method check
	claims := jwt.RegisteredClaims{Subject: "1"}
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	tokenStr, _ := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	_, err := VerifyToken(tokenStr, testAccessSecret)
	assert.Error(t, err)
}

func TestVerifyRefreshToken_UnexpectedSigningMethod(t *testing.T) {
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, &RefreshClaims{TokenType: "refresh"})
	tokenStr, _ := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	_, err := VerifyRefreshToken(tokenStr, testRefreshSecret)
	assert.Error(t, err)
}

func TestVerifyActivationToken_UnexpectedSigningMethod(t *testing.T) {
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, &ActivationClaims{TokenType: "activation"})
	tokenStr, _ := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	_, err := VerifyActivationToken(tokenStr, testActivationSecret)
	assert.Error(t, err)
}

func TestVerifyResetToken_WrongSecret(t *testing.T) {
	tok, _ := GenerateResetToken("reset@test.com", testActivationSecret)
	_, err := VerifyResetToken(tok, "wrong-secret")
	assert.Error(t, err)
}

func TestVerifyResetToken_UnexpectedSigningMethod(t *testing.T) {
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, &ResetClaims{TokenType: "reset"})
	tokenStr, _ := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	_, err := VerifyResetToken(tokenStr, testActivationSecret)
	assert.Error(t, err)
}
