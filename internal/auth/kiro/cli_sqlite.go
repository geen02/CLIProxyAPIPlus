// Package kiro provides authentication for Kiro CLI via SQLite database.
package kiro

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
	log "github.com/sirupsen/logrus"
)

// SQLite token keys (searched in priority order)
var sqliteTokenKeys = []string{
	"kirocli:social:token",     // Social login (Google, GitHub, Microsoft, etc.)
	"kirocli:odic:token",       // AWS SSO OIDC (kiro-cli corporate)
	"codewhisperer:odic:token", // Legacy AWS SSO OIDC
}

// Device registration keys (for AWS SSO OIDC only)
var sqliteRegistrationKeys = []string{
	"kirocli:odic:device-registration",
	"codewhisperer:odic:device-registration",
}

// KiroCLITokenData represents the token data stored in kiro-cli SQLite database.
type KiroCLITokenData struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	ProfileArn   string   `json:"profile_arn,omitempty"`
	ExpiresAt    string   `json:"expires_at"`
	Region       string   `json:"region,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
}

// KiroCLIDeviceRegistration represents device registration data for AWS SSO OIDC.
type KiroCLIDeviceRegistration struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Region       string `json:"region,omitempty"`
}

// LoadKiroCLIToken loads token data from kiro-cli SQLite database.
// It searches for tokens in priority order and returns the first valid token found.
func LoadKiroCLIToken(dbPath string) (*KiroTokenData, error) {
	// Expand home directory if needed
	if dbPath[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		dbPath = filepath.Join(home, dbPath[2:])
	}

	// Check if database exists
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("kiro-cli database not found: %s", dbPath)
	}

	// Open SQLite database
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	// Try to load token from any of the supported keys
	var tokenData *KiroCLITokenData
	var tokenKey string
	for _, key := range sqliteTokenKeys {
		var value string
		err := db.QueryRow("SELECT value FROM auth_kv WHERE key = ?", key).Scan(&value)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			log.Debugf("Error reading token key %s: %v", key, err)
			continue
		}

		// Parse JSON
		var td KiroCLITokenData
		if err := json.Unmarshal([]byte(value), &td); err != nil {
			log.Debugf("Error parsing token data for key %s: %v", key, err)
			continue
		}

		tokenData = &td
		tokenKey = key
		log.Debugf("Loaded credentials from SQLite key: %s", key)
		break
	}

	if tokenData == nil {
		return nil, fmt.Errorf("no valid token found in database (tried keys: %v)", sqliteTokenKeys)
	}

	// Try to load device registration (for AWS SSO OIDC)
	var deviceReg *KiroCLIDeviceRegistration
	for _, key := range sqliteRegistrationKeys {
		var value string
		err := db.QueryRow("SELECT value FROM auth_kv WHERE key = ?", key).Scan(&value)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			log.Debugf("Error reading device registration key %s: %v", key, err)
			continue
		}

		// Parse JSON
		var dr KiroCLIDeviceRegistration
		if err := json.Unmarshal([]byte(value), &dr); err != nil {
			log.Debugf("Error parsing device registration for key %s: %v", key, err)
			continue
		}

		deviceReg = &dr
		log.Debugf("Loaded device registration from SQLite key: %s", key)
		break
	}

	// Determine auth method based on token key and device registration
	authMethod := "social"
	if deviceReg != nil && (tokenKey == "kirocli:odic:token" || tokenKey == "codewhisperer:odic:token") {
		authMethod = "builder-id"
	}

	// Determine region (prefer device registration region, fallback to token region)
	region := "us-east-1"
	if deviceReg != nil && deviceReg.Region != "" {
		region = deviceReg.Region
	} else if tokenData.Region != "" {
		region = tokenData.Region
	}

	// Build KiroTokenData
	result := &KiroTokenData{
		AccessToken:  tokenData.AccessToken,
		RefreshToken: tokenData.RefreshToken,
		ProfileArn:   tokenData.ProfileArn,
		ExpiresAt:    tokenData.ExpiresAt,
		AuthMethod:   authMethod,
		Provider:     "kiro-cli",
		Region:       region,
	}

	// Add device registration data if available
	if deviceReg != nil {
		result.ClientID = deviceReg.ClientID
		result.ClientSecret = deviceReg.ClientSecret
	}

	// Extract email from JWT if available
	if result.Email == "" {
		result.Email = ExtractEmailFromJWT(result.AccessToken)
	}

	log.Infof("Successfully loaded kiro-cli credentials from: %s", dbPath)
	return result, nil
}

// GetDefaultKiroCLIDBPath returns the default path to kiro-cli SQLite database.
func GetDefaultKiroCLIDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	// Try kiro-cli path first
	kiroPath := filepath.Join(home, ".local", "share", "kiro-cli", "data.sqlite3")
	if _, err := os.Stat(kiroPath); err == nil {
		return kiroPath
	}

	// Try amazon-q-developer-cli path
	amazonQPath := filepath.Join(home, ".local", "share", "amazon-q", "data.sqlite3")
	if _, err := os.Stat(amazonQPath); err == nil {
		return amazonQPath
	}

	// Return kiro-cli path as default even if it doesn't exist
	return kiroPath
}
