package cache_client

import (
	"encoding/json"
	"fmt"
	"milton_prism/core/shared/mailer"
	paniccontrol "milton_prism/core/shared/utils"
	applog "milton_prism/pkg/log"
)

// This constant defines the prefix for keys in the cache specific to email verification.
const emailVerifyCachePrefix = "milton_prism_email_verify_cf:"

// EmailVerifyCache wraps your generic Cache to specifically handle email verification data.
type EmailVerifyCache struct {
	*Cache // Assuming 'Cache' is your Redigo/Redis pool wrapper
	// ttl time.Duration // This field is actually not needed here if TTL is passed directly to AddEmailToVerify
}

// NewEmailVerifyCache creates a new instance of EmailVerifyCache.
// It takes a pointer to your generic Cache pool.
func NewEmailVerifyCache(pool *Cache) *EmailVerifyCache {
	return &EmailVerifyCache{
		Cache: pool,
	}
}

// AddEmailToVerify stores email verification data in the cache.
// It uses the verification credential (token, OTP, or code) as part of the key.
// 'd' is the VerificationData containing UserID, Email, Method, Credential, etc.
// 'ttl' is the time-to-live for this cache entry in seconds.
func (r *EmailVerifyCache) AddEmailToVerify(d mailer.VerificationData, ttl uint64) error {
	conn := r.GetConn()
	defer func() {
		if err := conn.Close(); err != nil {
			applog.Warningf("cache: connection close: error=%v", err)
		}
		paniccontrol.RecoverFromPanic()
	}()

	cacheKey := fmt.Sprintf("%s%s", emailVerifyCachePrefix, d.Credential)

	data, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("failed to marshal verification data: %w", err)
	}

	// Store data with TTL (SETEX key seconds value)
	// We convert ttl (uint64) to int64, which is expected by Redigo's Do method for seconds.
	if _, err := conn.Do("SETEX", cacheKey, int64(ttl), data); err != nil {
		return fmt.Errorf("failed to set email verification data in cache: %w", err)
	}

	return nil
}

// GetEmailToVerify retrieves email verification data from the cache using the credential (token/OTP/code).
// 'submittedCredential' refers to the unique token, OTP, or code that was sent to the user.
func (r *EmailVerifyCache) GetEmailToVerify(submittedCredential string) (mailer.VerificationData, error) {

	conn := r.GetConn()
	defer func() {
		if err := conn.Close(); err != nil {
			applog.Warningf("cache: connection close: error=%v", err)
		}
		paniccontrol.RecoverFromPanic()
	}()

	cacheKey := fmt.Sprintf("%s%s", emailVerifyCachePrefix, submittedCredential)

	// Retrieve data from Redis (GET key)
	// conn.Do returns an interface{}, so we need to cast it to []byte for JSON unmarshaling.
	data, err := conn.Do("GET", cacheKey)
	if err != nil {
		return mailer.VerificationData{}, fmt.Errorf("failed to get email verification data from cache: %w", err)
	}

	// If data is nil, it means the key was not found or has expired.
	if data == nil {
		return mailer.VerificationData{}, fmt.Errorf("email verification data not found or expired for submittedCredential: %s", submittedCredential)
	}

	var d mailer.VerificationData
	// Unmarshal the JSON byte slice into the VerificationData struct.
	if err := json.Unmarshal(data.([]byte), &d); err != nil {
		return mailer.VerificationData{}, fmt.Errorf("failed to unmarshal verification data: %w", err)
	}

	return d, nil
}

// DeleteEmailVerificationData removes a verification entry from the cache.
// This is typically called after successful verification to prevent reuse.
func (r *EmailVerifyCache) DeleteEmailVerificationData(credential string) error {
	conn := r.GetConn()
	defer func() {
		if err := conn.Close(); err != nil {
			applog.Warningf("cache: connection close: error=%v", err)
		}
		paniccontrol.RecoverFromPanic()
	}()

	cacheKey := fmt.Sprintf("%s%s", emailVerifyCachePrefix, credential)

	// Delete key from Redis
	if _, err := conn.Do("DEL", cacheKey); err != nil {
		return fmt.Errorf("failed to delete email verification data from cache: %w", err)
	}
	return nil
}
