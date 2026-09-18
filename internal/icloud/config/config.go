// Package config holds the iCloud-privacy-mail module runtime configuration.
//
// This module is integrated into gpt-go and stores all state in MongoDB
// (migrated from the original SQLite backend). The sqlite-specific fields from
// the upstream project (data_path, database_backup_dir, ...) are removed; the
// remaining fields govern the Apple/iCloud protocol behaviour, background
// workers and public API.
package config

// Config holds the iCloud module tunables. Values come from Default() and may
// be overridden by the admin through the settings page (persisted in MongoDB).
type Config struct {
	SessionTTLHours                    int    `json:"session_ttl_hours"`
	SecureCookie                       bool   `json:"secure_cookie"`
	APIKey                             string `json:"api_key"`
	PublicBaseURL                      string `json:"public_base_url"`
	ICloudDefaultHost                  string `json:"icloud_default_host"`
	ICloudClientID                     string `json:"icloud_client_id"`
	AppleAccountAPIKey                 string `json:"apple_account_api_key"`
	AppleAccountKeepAliveEnabled       bool   `json:"apple_account_keep_alive_enabled"`
	AppleAccountKeepAliveMS            int    `json:"apple_account_keep_alive_ms"`
	AppleAccountKeepAliveJitterPercent int    `json:"apple_account_keep_alive_jitter_percent"`
	MailWatcherEnabled                 bool   `json:"mail_watcher_enabled"`
	MailWatcherPollMS                  int    `json:"mail_watcher_poll_ms"`
	MailWatcherWebPollMS               int    `json:"mail_watcher_web_poll_ms"`
	MailWatcherFetchLimit              int    `json:"mail_watcher_fetch_limit"`
	MailWatcherInitialFetchLimit       int    `json:"mail_watcher_initial_fetch_limit"`
	MailWatcherLookbackHours           int    `json:"mail_watcher_lookback_hours"`
	PublicFastSyncWaitMS               int    `json:"public_fast_sync_wait_ms"`
	PublicSyncMinIntervalMS            int    `json:"public_sync_min_interval_ms"`
	PublicMailboxLeaseTTLMinutes       int    `json:"public_mailbox_lease_ttl_minutes"`
	PublicMailboxLeaseMaxTTLMinutes    int    `json:"public_mailbox_lease_max_ttl_minutes"`
	PublicMailboxLeaseSweepSeconds     int    `json:"public_mailbox_lease_sweep_seconds"`
	DatabaseMessageRetentionDays       int    `json:"database_message_retention_days"`
	DatabaseChangeLogLimit             int    `json:"database_change_log_limit"`
	UpdateEnabled                      bool   `json:"update_enabled"`
	UpdateRepository                   string `json:"update_repository"`
	ServerChanSendKey                  string `json:"server_chan_send_key"`
	ServerChanHideIP                   bool   `json:"server_chan_hide_ip"`
	ServerChanNotifyAdminLogin         bool   `json:"server_chan_notify_admin_login"`
	ServerChanNotifyLoginStateOffline  bool   `json:"server_chan_notify_login_state_offline"`
}

// Default returns the built-in iCloud module configuration.
func Default() Config {
	return Config{
		SessionTTLHours:                    24 * 7,
		SecureCookie:                       false,
		ICloudDefaultHost:                  "www.icloud.com.cn",
		ICloudClientID:                     "d39ba9916b7251055b22c7f910e2ea796ee65e98b2ddecea8f5dde8d9d1a815d",
		AppleAccountKeepAliveEnabled:       true,
		AppleAccountKeepAliveMS:            180000,
		AppleAccountKeepAliveJitterPercent: 15,
		MailWatcherEnabled:                 true,
		MailWatcherPollMS:                  3000,
		MailWatcherWebPollMS:               60000,
		MailWatcherFetchLimit:              8,
		MailWatcherInitialFetchLimit:       20,
		MailWatcherLookbackHours:           24,
		PublicFastSyncWaitMS:               600,
		PublicSyncMinIntervalMS:            3000,
		PublicMailboxLeaseTTLMinutes:       30,
		PublicMailboxLeaseMaxTTLMinutes:    7 * 24 * 60,
		PublicMailboxLeaseSweepSeconds:     30,
		DatabaseMessageRetentionDays:       90,
		DatabaseChangeLogLimit:             5000,
		UpdateEnabled:                      true,
		UpdateRepository:                   "xiuxiu56/iCloud-Privacy-Mail-v2",
		ServerChanHideIP:                   true,
	}
}
