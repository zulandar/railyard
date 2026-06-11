package models

import "time"

// PluginKV is one row in the plugin-scoped persistent key/value store
// (railyard-77h.11). Plugins reach it exclusively through the host's
// HostService KV RPCs; the namespace (Plugin) is bound to the calling
// plugin's connection identity by the host and is NEVER supplied by the
// plugin, so cross-plugin reads are impossible by construction.
//
// The primary key is the composite (Plugin, Key): a key is unique only
// within a plugin's namespace, so two different plugins may both store a
// row under the same Key without collision.
type PluginKV struct {
	// Plugin is the owning plugin's stable name (the connection-bound
	// identity on the host side). Part of the composite primary key.
	Plugin string `gorm:"primaryKey;size:256"`

	// Key is the plugin-chosen key. Part of the composite primary key.
	// The host caps the byte length at KVMaxKeyBytes.
	Key string `gorm:"primaryKey;size:256"`

	// Value is the opaque payload. Plugins choose their own encoding; the
	// host imposes only a size cap (KVMaxValueBytes), not a format. The
	// column is mediumblob (MySQL max 16 MiB) so a max-size value
	// (KVMaxValueBytes = 64 KiB) fits comfortably — a plain blob caps at
	// 65535 bytes and would reject a 64 KiB value the size check accepted.
	Value []byte `gorm:"type:mediumblob"`

	// UpdatedAt is the wall-clock time of the last write. GORM maintains
	// it automatically on Create/Save.
	UpdatedAt time.Time
}
