// Package engine implements the worker agent daemon.
package engine

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/zulandar/railyard/internal/messaging"
	"github.com/zulandar/railyard/internal/models"
	"gorm.io/gorm"
)

// Engine status constants.
const (
	StatusIdle    = "idle"
	StatusWorking = "working"
	StatusStalled = "stalled"
	StatusDead    = "dead"
)

// RegisterOpts holds parameters for registering an engine.
type RegisterOpts struct {
	Track     string
	Role      string
	PodName   string
	SessionID string
	Provider  string // agent provider name (e.g., "claude", "codex")
}

// GenerateID creates a unique engine ID in eng-xxxxxxxx format (8-char hex).
func GenerateID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("engine: generate ID: %w", err)
	}
	return "eng-" + hex.EncodeToString(b), nil
}

// generateUniqueID generates an ID and retries once on collision.
func generateUniqueID(db *gorm.DB) (string, error) {
	for range 2 {
		id, err := GenerateID()
		if err != nil {
			return "", err
		}
		var count int64
		if err := db.Model(&models.Engine{}).Where("id = ?", id).Count(&count).Error; err != nil {
			return "", fmt.Errorf("engine: check ID uniqueness: %w", err)
		}
		if count == 0 {
			return id, nil
		}
	}
	return "", fmt.Errorf("engine: failed to generate unique ID after retries")
}

// Register creates a new engine record with status=idle.
func Register(db *gorm.DB, opts RegisterOpts) (*models.Engine, error) {
	if opts.Track == "" {
		return nil, fmt.Errorf("engine: track is required")
	}

	if opts.Role == "" {
		opts.Role = "engine"
	}

	id, err := generateUniqueID(db)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	engine := models.Engine{
		ID:           id,
		PodName:      opts.PodName,
		Track:        opts.Track,
		Role:         opts.Role,
		Status:       StatusIdle,
		SessionID:    opts.SessionID,
		Provider:     opts.Provider,
		StartedAt:    now,
		LastActivity: now,
	}

	if err := db.Create(&engine).Error; err != nil {
		return nil, fmt.Errorf("engine: register: %w", err)
	}

	// Ack any pre-existing broadcast backlog so a freshly registered engine
	// only obeys broadcasts sent after it came online. Without this, a stale
	// "drain" broadcast left in the messages table by a prior `ry stop` would
	// shut the new engine down on its first poll cycle (railyard-d3n).
	ackBroadcastBacklog(db, engine.ID)

	return &engine, nil
}

// ackBroadcastBacklog marks every broadcast message that already exists as
// acknowledged for engineID, so [ProcessInbox] does not surface them. Done at
// registration time (railyard-d3n). Best-effort: a failure here at worst
// leaves the prior behavior intact, so it must never fail registration.
func ackBroadcastBacklog(db *gorm.DB, engineID string) {
	var ids []uint
	if err := db.Model(&models.Message{}).
		Where("to_agent = ?", "broadcast").
		Pluck("id", &ids).Error; err != nil {
		return
	}
	for _, id := range ids {
		_ = messaging.AcknowledgeBroadcast(db, id, engineID)
	}
}

// Deregister marks an engine as dead.
func Deregister(db *gorm.DB, engineID string) error {
	var engine models.Engine
	if err := db.Where("id = ?", engineID).First(&engine).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("engine: not found: %s", engineID)
		}
		return fmt.Errorf("engine: get %s for deregister: %w", engineID, err)
	}

	if err := db.Model(&models.Engine{}).Where("id = ?", engineID).Update("status", StatusDead).Error; err != nil {
		return fmt.Errorf("engine: deregister %s: %w", engineID, err)
	}
	return nil
}

// Get retrieves an engine by ID.
func Get(db *gorm.DB, engineID string) (*models.Engine, error) {
	var engine models.Engine
	if err := db.Where("id = ?", engineID).First(&engine).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("engine: not found: %s", engineID)
		}
		return nil, fmt.Errorf("engine: get %s: %w", engineID, err)
	}
	return &engine, nil
}
