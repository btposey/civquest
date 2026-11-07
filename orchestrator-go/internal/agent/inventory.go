package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// CachedItem represents an individual inventory item slot snapshot.
type CachedItem struct {
	Slot          int32  `json:"slot"`
	ItemID        string `json:"item_id"`
	Count         int32  `json:"count"`
	Durability    int32  `json:"durability,omitempty"`
	MaxDurability int32  `json:"max_durability,omitempty"`
	CustomName    string `json:"custom_name,omitempty"`
}

// InventoryCache encapsulates the full inventory cache file format
// stored at $XDG_DATA_HOME/civquest/agents/{agent_id}/inventory_cache.json.
type InventoryCache struct {
	AgentID   string       `json:"agent_id"`
	WorldTick int64        `json:"world_tick"`
	UpdatedAt time.Time    `json:"updated_at"`
	Items     []CachedItem `json:"items"`
}

// NewInventoryCache builds an InventoryCache snapshot from incoming Protobuf ItemStackProto items.
func NewInventoryCache(agentID string, worldTick int64, items []*civquestv1.ItemStackProto) *InventoryCache {
	cached := make([]CachedItem, 0, len(items))
	for _, it := range items {
		if it == nil {
			continue
		}
		cached = append(cached, CachedItem{
			Slot:          it.Slot,
			ItemID:        it.ItemId,
			Count:         it.Count,
			Durability:    it.Durability,
			MaxDurability: it.MaxDurability,
			CustomName:    it.CustomName,
		})
	}

	return &InventoryCache{
		AgentID:   agentID,
		WorldTick: worldTick,
		UpdatedAt: time.Now().UTC(),
		Items:     cached,
	}
}

// WriteInventoryCache writes cache to targetPath atomically via a temp file and rename.
func WriteInventoryCache(targetPath string, cache *InventoryCache) error {
	if cache == nil {
		return fmt.Errorf("inventory cache cannot be nil")
	}

	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("creating directory for inventory cache %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling inventory cache: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, "inventory_cache_*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp inventory file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer func() {
		// Clean up temporary file if rename did not happen
		_ = os.Remove(tmpName)
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("writing temporary inventory cache: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("syncing temporary inventory cache: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing temporary inventory cache: %w", err)
	}

	if err := os.Rename(tmpName, targetPath); err != nil {
		return fmt.Errorf("renaming temp inventory cache to %s: %w", targetPath, err)
	}

	return nil
}

// ReadInventoryCache parses the inventory cache from targetPath.
func ReadInventoryCache(targetPath string) (*InventoryCache, error) {
	data, err := os.ReadFile(targetPath)
	if err != nil {
		return nil, fmt.Errorf("reading inventory cache from %s: %w", targetPath, err)
	}

	var cache InventoryCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, fmt.Errorf("unmarshaling inventory cache from %s: %w", targetPath, err)
	}

	return &cache, nil
}

// InventoryEqual compares two cached item slices for semantic equality.
func InventoryEqual(a, b []CachedItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Slot != b[i].Slot ||
			a[i].ItemID != b[i].ItemID ||
			a[i].Count != b[i].Count ||
			a[i].Durability != b[i].Durability ||
			a[i].MaxDurability != b[i].MaxDurability ||
			a[i].CustomName != b[i].CustomName {
			return false
		}
	}
	return true
}
