package db

import (
	"context"
	"database/sql"
	"sort"
)

// Settings is a tiny key-value store for global app config the UI toggles at
// runtime (e.g. traffic_capture). Missing keys fall back to caller defaults.

// GetSetting returns the stored value and ok=false when the key is unset.
func (d *DB) GetSetting(key string) (value string, ok bool, err error) {
	err = d.QueryRow(`SELECT value FROM settings WHERE key=$1`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// SetSetting upserts a setting value.
func (d *DB) SetSetting(key, value string) error {
	return d.SetSettingContext(context.Background(), key, value)
}

// SetSettingContext upserts a setting and respects cancellation or a deadline.
func (d *DB) SetSettingContext(ctx context.Context, key, value string) error {
	_, err := d.ExecContext(ctx, `
INSERT INTO settings(key, value) VALUES ($1, $2)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, value)
	return err
}

// SetSettingsAtomic upserts several settings in one transaction. Keys are
// sorted so concurrent callers acquire row locks in a deterministic order.
func (d *DB) SetSettingsAtomic(ctx context.Context, values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // commit makes this a no-op
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO settings(key, value) VALUES ($1, $2)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, values[key]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// InsertSettingIfAbsent writes only when the key does not exist; otherwise it preserves the value and returns inserted=false.
// Use for keys such as auth.password_hash that may only be set once: the database primary-key constraint enforces this,
// while a caller-side GetSetting followed by a write is only a fast-failure path—if the read fails or concurrent writes race,
// the existing value still cannot be accidentally overwritten by ON CONFLICT DO UPDATE.
func (d *DB) InsertSettingIfAbsent(key, value string) (inserted bool, err error) {
	res, err := d.Exec(`
INSERT INTO settings(key, value) VALUES ($1, $2)
ON CONFLICT (key) DO NOTHING`, key, value)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// GetBool returns the boolean setting, or def when unset/unparseable.
func (d *DB) GetBool(key string, def bool) bool {
	v, ok, err := d.GetSetting(key)
	if err != nil || !ok {
		return def
	}
	return v == "true" || v == "1"
}

// SetBool stores a boolean setting as "true"/"false".
func (d *DB) SetBool(key string, val bool) error {
	if val {
		return d.SetSetting(key, "true")
	}
	return d.SetSetting(key, "false")
}
