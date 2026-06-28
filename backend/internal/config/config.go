package config

import "os"

type Config struct {
	HTTPPort                 string
	DBPath                   string
	NPSBaseURL               string
	NPSAuthKey               string
	NPSAuthCryptKey          string
	OpenCodeTimeoutMS        string
	WSHeartbeatMS            string
	ReminderCheckIntervalSec string
	AndroidAppID             string
	UseAndroidShell          string
	OpenCodeInstancesJSON    string
}

func Load() Config {
	return Config{
		HTTPPort:                 getEnv("POCKET_HTTP_PORT", "8088"),
		DBPath:                   getEnv("POCKET_DB_PATH", "./data/pocket.sqlite"),
		NPSBaseURL:               getFirstEnv([]string{"POCKET_INSTANCE_DISCOVERY_BASE_URL", "POCKET_NPS_BASE_URL"}, ""),
		NPSAuthKey:               getFirstEnv([]string{"POCKET_INSTANCE_DISCOVERY_AUTH_TOKEN", "POCKET_NPS_AUTH_KEY"}, ""),
		NPSAuthCryptKey:          getFirstEnv([]string{"POCKET_INSTANCE_DISCOVERY_AUTH_SECRET", "POCKET_NPS_AUTH_CRYPT_KEY"}, ""),
		OpenCodeTimeoutMS:        getEnv("POCKET_OPENCODE_TIMEOUT_MS", "5000"),
		WSHeartbeatMS:            getEnv("POCKET_WS_HEARTBEAT_MS", "15000"),
		ReminderCheckIntervalSec: getEnv("POCKET_REMINDER_CHECK_INTERVAL_SEC", "60"),
		AndroidAppID:             getEnv("POCKET_ANDROID_APP_ID", "com.kaixuan.opencode.pocket"),
		UseAndroidShell:          getEnv("POCKET_ANDROID_USE_CAPACITOR", "true"),
		OpenCodeInstancesJSON:    getFirstEnv([]string{"POCKET_INSTANCE_CATALOG_JSON", "POCKET_OPENCODE_INSTANCES"}, ""),
	}
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getFirstEnv(keys []string, fallback string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return fallback
}
