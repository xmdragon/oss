package auth

import (
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// GenerateTOTP returns a new secret + the otpauth:// URL for QR-code
// enrollment. The secret is what gets stored in admin.env; the URL is what we
// render as a QR code at setup time.
func GenerateTOTP(issuer, account string) (secret string, otpauthURL string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: account,
		Algorithm:   otp.AlgorithmSHA1, // RFC 6238 default; widest authenticator support
		Digits:      otp.DigitsSix,
		Period:      30,
	})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

// VerifyTOTP returns true if `code` is valid right now for `secret`. Allows
// ±1 step (30s) skew, which `totp.Validate` does by default.
func VerifyTOTP(code, secret string) bool {
	return totp.Validate(code, secret)
}
