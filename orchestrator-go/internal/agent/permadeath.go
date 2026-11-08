package agent

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
	_ "modernc.org/sqlite"
)

// PermadeathRequest holds parameters for executing an agent's terminal permadeath lifecycle.
type PermadeathRequest struct {
	AgentID           string
	AgentUUID         string
	ClanID            string
	DeathEvent        *civquestv1.DeathEvent
	Resolver          *xdg.Resolver
	AgentDB           *db.AgentDB
	ReputationDB      *db.ReputationDB
	ChroniclerService *system2.ChroniclerService
}

// PermadeathResult contains paths and outcomes of the permadeath teardown and archiving.
type PermadeathResult struct {
	GraveyardDir              string  `json:"graveyard_dir"`
	DeathCertificatePath      string  `json:"death_certificate_path"`
	EpitaphPath               string  `json:"epitaph_path"`
	MemoryDBPath              string  `json:"memory_db_path"`
	MemorialMemoryID          int64   `json:"memorial_memory_id"`
	SpouseUpdated             bool    `json:"spouse_updated"`
	PrestigeDeducted          float64 `json:"prestige_deducted"`
	PrestigeTargetPlayer      string  `json:"prestige_target_player,omitempty"`
	CompanionMemorialWritten  bool    `json:"companion_memorial_written,omitempty"`
	CompanionMemorialPath     string  `json:"companion_memorial_path,omitempty"`
	CompanionMemorialTitle    string  `json:"companion_memorial_title,omitempty"`
	CompanionMemorialMemoryID int64   `json:"companion_memorial_memory_id,omitempty"`
}

// DeathLocation encapsulates 3D death coordinates for JSON serialization.
type DeathLocation struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

// DroppedLootItem represents an inventory item dropped upon permadeath.
type DroppedLootItem struct {
	ItemID     string `json:"item_id"`
	Count      int32  `json:"count"`
	Slot       int32  `json:"slot,omitempty"`
	Durability int32  `json:"durability,omitempty"`
	CustomName string `json:"custom_name,omitempty"`
}

// DeathCertificate documents the official autopsy, inventory drops, and circumstances of death.
type DeathCertificate struct {
	AgentID           string            `json:"agent_id"`
	OfflineUUID       string            `json:"offline_uuid"`
	DeathTick         int64             `json:"death_tick"`
	DamageSource      string            `json:"damage_source"`
	KillerEntityID    int32             `json:"killer_entity_id"`
	KillerUUID        string            `json:"killer_uuid"`
	KillerDisplayName string            `json:"killer_display_name"`
	DeathLocation     DeathLocation     `json:"death_location"`
	DroppedLoot       []DroppedLootItem `json:"dropped_loot"`
	ArchivedAt        time.Time         `json:"archived_at"`
}

// OfflinePlayerUUID generates the deterministic RFC 4122 v3 UUID for Minecraft offline players.
// It reproduces Java's UUID.nameUUIDFromBytes(("OfflinePlayer:" + name).getBytes(StandardCharsets.UTF_8)).
func OfflinePlayerUUID(name string) string {
	data := []byte("OfflinePlayer:" + name)
	hash := md5.Sum(data)
	hash[6] = (hash[6] & 0x0f) | 0x30 // RFC 4122 Version 3
	hash[8] = (hash[8] & 0x3f) | 0x80 // RFC 4122 Variant 1 (0b10xxxxxx)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		hash[0:4], hash[4:6], hash[6:8], hash[8:10], hash[10:16])
}

// ExecutePermadeath coordinates the five-step terminal permadeath lifecycle:
// 1. Logs a Saliency 1.0 Memorial Record into memory.db.
// 2. Updates any active spouse relationships to WIDOWED (-10 disposition).
// 3. Deducts Player Prestige if murdered (-100.0) or if a companion perished (-50.0).
// 4. Closes AgentDB with WAL checkpoint (TRUNCATE) to release all locks.
// 5. Cold-archives the agent directory to the graveyard with death certificate, epitaph, and read-only memory.db.
func ExecutePermadeath(ctx context.Context, req PermadeathRequest) (*PermadeathResult, error) {
	if req.DeathEvent == nil {
		return nil, errors.New("death event cannot be nil")
	}
	if req.AgentID == "" {
		return nil, errors.New("agent ID cannot be empty")
	}

	r := req.Resolver
	if r == nil {
		r = xdg.DefaultResolver()
	}

	agentUUID := req.AgentUUID
	if agentUUID == "" {
		agentUUID = OfflinePlayerUUID(req.AgentID)
	}

	clanID := req.ClanID
	if clanID == "" {
		clanID = "pioneers"
	}

	deathEv := req.DeathEvent
	worldTick := deathEv.GetWorldTick()

	result := &PermadeathResult{}

	// Ensure context is usable
	if ctx == nil || ctx.Err() != nil {
		ctx = context.Background()
	}

	// -------------------------------------------------------------
	// Step 1: Record Saliency 1.0 Memorial Record in AgentDB
	// -------------------------------------------------------------
	if req.AgentDB != nil {
		killer := deathEv.GetKillerDisplayName()
		if killer == "" {
			killer = deathEv.GetKillerUuid()
		}
		if killer == "" {
			killer = "none"
		}

		obituarySummary := fmt.Sprintf("%s perished at tick %d by %s (killer: %s)",
			req.AgentID, worldTick, deathEv.GetDamageSource(), killer)

		actors := []string{req.AgentID}
		if deathEv.GetKillerDisplayName() != "" {
			actors = append(actors, deathEv.GetKillerDisplayName())
		} else if deathEv.GetKillerUuid() != "" {
			actors = append(actors, deathEv.GetKillerUuid())
		}
		actorsJSONBytes, _ := json.Marshal(actors)

		locCoords := "0.0,0.0,0.0"
		if loc := deathEv.GetDeathLocation(); loc != nil {
			locCoords = fmt.Sprintf("%.1f,%.1f,%.1f", loc.GetX(), loc.GetY(), loc.GetZ())
		}

		memID, err := req.AgentDB.InsertEpisodicMemory(ctx, db.EpisodicMemory{
			WorldTick:      worldTick,
			EventCategory:  "DEATH",
			SaliencyScore:  1.0,
			SummaryText:    obituarySummary,
			ActorsJSON:     string(actorsJSONBytes),
			LocationCoords: locCoords,
			Chronicled:     false,
		}, nil)
		if err != nil {
			log.Printf("[WARN] agent %s failed to record memorial episodic memory: %v", req.AgentID, err)
		} else {
			result.MemorialMemoryID = memID
		}
	}

	// -------------------------------------------------------------
	// Step 2: Update spouse status to WIDOWED in AgentDB and living spouse DB
	// -------------------------------------------------------------
	var activeRelationships []db.Relationship
	if req.AgentDB != nil {
		rels, err := req.AgentDB.ListRelationships(ctx)
		if err != nil {
			log.Printf("[WARN] agent %s failed to list relationships: %v", req.AgentID, err)
		} else {
			activeRelationships = rels
			for _, rel := range rels {
				if strings.EqualFold(rel.KinshipRole, "SPOUSE") {
					rel.KinshipRole = "WIDOWED"
					rel.DispositionScore = -10.0
					rel.LastInteractionTick = worldTick
					if err := req.AgentDB.UpsertRelationship(ctx, rel); err != nil {
						log.Printf("[WARN] agent %s failed to update spouse to WIDOWED: %v", req.AgentID, err)
					} else {
						result.SpouseUpdated = true
					}

					// Update living spouse's memory.db if they are an active agent
					if strings.EqualFold(rel.EntityType, "AGENT") && rel.EntityName != "" {
						spouseDBPath := r.AgentMemoryDBPath(rel.EntityName)
						if _, statErr := os.Stat(spouseDBPath); statErr == nil {
							if sdb, openErr := sql.Open("sqlite", spouseDBPath); openErr == nil {
								_, _ = sdb.ExecContext(ctx, `
									UPDATE relationships
									SET kinship_role = 'WIDOWED',
									    disposition_score = MAX(-100.0, disposition_score - 10.0),
									    last_interaction_tick = ?
									WHERE entity_name = ? OR entity_uuid = ?;
								`, worldTick, req.AgentID, agentUUID)
								_ = sdb.Close()
							}
						}
					}
				}
			}
		}
	}

	// -------------------------------------------------------------
	// Step 3: Deduct Player Prestige & Record Companion Memorial Chronicle
	// -------------------------------------------------------------
	var companionMemorialTitle, companionMemorialProse string

	// Find active bonded companion player if any: must meet BondCompanion threshold
	var companion *db.Relationship
	for i := range activeRelationships {
		rel := &activeRelationships[i]
		if strings.EqualFold(rel.EntityType, "PLAYER") && (rel.DispositionScore >= system2.CompanionMinDisposition && rel.Familiarity >= system2.CompanionMinFamiliarity) {
			if companion == nil || rel.DispositionScore > companion.DispositionScore {
				companion = rel
			}
		}
	}

	if req.ReputationDB != nil {
		isPlayerMurder := false
		killerUUID := deathEv.GetKillerUuid()
		killerName := deathEv.GetKillerDisplayName()
		damageSource := strings.ToLower(deathEv.GetDamageSource())

		// A death is classified as player murder ONLY if:
		// 1) The damage source explicitly indicates player attack, OR
		// 2) killerUUID or killerName matches an entity registered as a PLAYER in active relationships.
		// Mob attacks (zombies, skeletons, creepers, etc.) have entity UUIDs in Minecraft
		// but must NOT be misclassified as player murders.
		if strings.Contains(damageSource, "player") {
			isPlayerMurder = true
		} else if killerUUID != "" || killerName != "" {
			for _, rel := range activeRelationships {
				if strings.EqualFold(rel.EntityType, "PLAYER") && (rel.EntityUUID == killerUUID || rel.EntityName == killerName) {
					isPlayerMurder = true
					break
				}
			}
		}

		if isPlayerMurder {
			targetUUID := killerUUID
			if targetUUID == "" {
				targetUUID = killerName
			}
			if targetUUID == "" && deathEv.GetKillerEntityId() > 0 {
				targetUUID = fmt.Sprintf("player-%d", deathEv.GetKillerEntityId())
			}
			if targetUUID != "" {
				displayName := killerName
				if displayName == "" {
					displayName = targetUUID
				}
				reason := fmt.Sprintf("Outlaw: Murdered agent %s", req.AgentID)
				if _, err := req.ReputationDB.AddPrestige(targetUUID, displayName, -100.0, reason, worldTick); err != nil {
					log.Printf("[WARN] agent %s failed to record murder prestige deduction: %v", req.AgentID, err)
				} else {
					result.PrestigeDeducted = -100.0
					result.PrestigeTargetPlayer = targetUUID
				}
			}
		} else if companion != nil {
			compName := companion.EntityName
			if compName == "" {
				compName = companion.EntityUUID
			}
			reason := "Companion perished under player protection"
			if _, err := req.ReputationDB.AddPrestige(companion.EntityUUID, compName, -50.0, reason, worldTick); err != nil {
				log.Printf("[WARN] agent %s failed to record companion prestige deduction: %v", req.AgentID, err)
			} else {
				result.PrestigeDeducted = -50.0
				result.PrestigeTargetPlayer = companion.EntityUUID
			}
		}
	}

	// If companion perished in battle, synthesize and record memorial chronicle in AgentDB
	if companion != nil && isBattleDeath(deathEv) {
		compName := companion.EntityName
		if compName == "" {
			compName = companion.EntityUUID
		}
		killer := deathEv.GetKillerDisplayName()
		if killer == "" {
			killer = deathEv.GetKillerUuid()
		}
		if killer == "" {
			killer = "hostile forces"
		}

		var memTitle, memProse string
		if req.ChroniclerService != nil {
			synthCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			t, p, _, err := req.ChroniclerService.SynthesizeCompanionMemorial(synthCtx, req.AgentID, clanID, compName, killer, deathEv.GetDamageSource(), worldTick)
			cancel()
			if err == nil {
				memTitle = t
				memProse = p
			}
		}
		if memTitle == "" {
			memTitle, memProse = system2.FallbackCompanionMemorialProse(req.AgentID, clanID, compName, killer, deathEv.GetDamageSource(), worldTick)
		}
		companionMemorialTitle = memTitle
		companionMemorialProse = memProse

		if req.AgentDB != nil {
			locCoords := "0.0,0.0,0.0"
			if loc := deathEv.GetDeathLocation(); loc != nil {
				locCoords = fmt.Sprintf("%.1f,%.1f,%.1f", loc.GetX(), loc.GetY(), loc.GetZ())
			}
			actors := []string{req.AgentID, compName}
			if killer != "hostile forces" && killer != "" {
				actors = append(actors, killer)
			}
			actorsJSONBytes, _ := json.Marshal(actors)
			summary := fmt.Sprintf("[Companion Memorial Chronicle] %s fell in battle defending %s against %s (%s)",
				req.AgentID, compName, killer, deathEv.GetDamageSource())
			compMemID, err := req.AgentDB.InsertEpisodicMemory(ctx, db.EpisodicMemory{
				WorldTick:      worldTick,
				EventCategory:  "DEATH",
				SaliencyScore:  1.0,
				SummaryText:    summary,
				ActorsJSON:     string(actorsJSONBytes),
				LocationCoords: locCoords,
				Chronicled:     true,
			}, nil)
			if err != nil {
				log.Printf("[WARN] agent %s failed recording companion memorial episodic memory: %v", req.AgentID, err)
			} else {
				result.CompanionMemorialMemoryID = compMemID
			}
		}
	}

	// -------------------------------------------------------------
	// Step 4: Checkpoint and Close AgentDB so WAL flushes and locks release
	// -------------------------------------------------------------
	if req.AgentDB != nil {
		if err := req.AgentDB.Checkpoint(ctx); err != nil {
			log.Printf("[WARN] agent %s failed WAL checkpoint: %v", req.AgentID, err)
		}
		if err := req.AgentDB.Close(); err != nil {
			log.Printf("[WARN] agent %s failed closing agent DB: %v", req.AgentID, err)
		}
	}

	// -------------------------------------------------------------
	// Step 5: Cold Graveyard Archival
	// -------------------------------------------------------------
	sourceDir := r.AgentDataDir(req.AgentID)
	targetDir, err := r.GraveyardAgentDir(req.AgentID, agentUUID, worldTick)
	if err != nil {
		return nil, fmt.Errorf("resolving graveyard agent dir: %w", err)
	}
	result.GraveyardDir = targetDir

	// Ensure graveyard base directory exists
	if err := os.MkdirAll(r.GraveyardDir(), 0750); err != nil {
		return nil, fmt.Errorf("creating graveyard directory %s: %w", r.GraveyardDir(), err)
	}

	// Relocate agent directory: try atomic os.Rename, fallback to copy-and-remove if cross-device
	if fi, statErr := os.Stat(sourceDir); statErr == nil && fi.IsDir() {
		_ = os.RemoveAll(targetDir)
		if renameErr := os.Rename(sourceDir, targetDir); renameErr != nil {
			if copyErr := copyDir(sourceDir, targetDir); copyErr != nil {
				return nil, fmt.Errorf("relocating agent directory from %s to %s failed (rename: %v, copy: %w)",
					sourceDir, targetDir, renameErr, copyErr)
			}
			_ = os.RemoveAll(sourceDir)
		}
	} else {
		if err := os.MkdirAll(targetDir, 0750); err != nil {
			return nil, fmt.Errorf("creating graveyard target directory %s: %w", targetDir, err)
		}
	}

	// Generate and write death_certificate.json
	certPath, err := r.GraveyardDeathCertificatePath(req.AgentID, agentUUID, worldTick)
	if err != nil {
		return nil, fmt.Errorf("resolving death certificate path: %w", err)
	}
	result.DeathCertificatePath = certPath

	var loc DeathLocation
	if deathEv.GetDeathLocation() != nil {
		loc = DeathLocation{
			X: deathEv.GetDeathLocation().GetX(),
			Y: deathEv.GetDeathLocation().GetY(),
			Z: deathEv.GetDeathLocation().GetZ(),
		}
	}

	droppedLoot := make([]DroppedLootItem, 0, len(deathEv.GetDroppedLoot()))
	for _, item := range deathEv.GetDroppedLoot() {
		if item != nil {
			droppedLoot = append(droppedLoot, DroppedLootItem{
				ItemID:     item.GetItemId(),
				Count:      item.GetCount(),
				Slot:       item.GetSlot(),
				Durability: item.GetDurability(),
				CustomName: item.GetCustomName(),
			})
		}
	}

	cert := DeathCertificate{
		AgentID:           req.AgentID,
		OfflineUUID:       agentUUID,
		DeathTick:         worldTick,
		DamageSource:      deathEv.GetDamageSource(),
		KillerEntityID:    deathEv.GetKillerEntityId(),
		KillerUUID:        deathEv.GetKillerUuid(),
		KillerDisplayName: deathEv.GetKillerDisplayName(),
		DeathLocation:     loc,
		DroppedLoot:       droppedLoot,
		ArchivedAt:        time.Now().UTC(),
	}

	certBytes, err := json.MarshalIndent(cert, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling death certificate: %w", err)
	}
	if err := os.WriteFile(certPath, certBytes, 0644); err != nil {
		return nil, fmt.Errorf("writing death certificate %s: %w", certPath, err)
	}

	// Generate and write epitaph.txt
	epitaphPath, err := r.GraveyardEpitaphPath(req.AgentID, agentUUID, worldTick)
	if err != nil {
		return nil, fmt.Errorf("resolving epitaph path: %w", err)
	}
	result.EpitaphPath = epitaphPath

	epitaphContent := fmt.Sprintf(
		"Here lies %s of clan %s.\nPerished at world tick %d by %s.\nMay their deeds be remembered in the clan chronicles.\n",
		req.AgentID, clanID, worldTick, deathEv.GetDamageSource(),
	)
	if err := os.WriteFile(epitaphPath, []byte(epitaphContent), 0644); err != nil {
		return nil, fmt.Errorf("writing epitaph %s: %w", epitaphPath, err)
	}

	// Generate and write companion_memorial_chronicle.txt if companion perished in battle
	if companionMemorialProse != "" {
		chroniclePath := filepath.Join(targetDir, "companion_memorial_chronicle.txt")
		chronicleContent := fmt.Sprintf("%s\n\n%s\n", companionMemorialTitle, companionMemorialProse)
		if err := os.WriteFile(chroniclePath, []byte(chronicleContent), 0644); err != nil {
			log.Printf("[WARN] agent %s failed writing companion memorial chronicle: %v", req.AgentID, err)
		} else {
			result.CompanionMemorialWritten = true
			result.CompanionMemorialPath = chroniclePath
			result.CompanionMemorialTitle = companionMemorialTitle
		}
	}

	// Set memory.db and map_graph.db files in graveyard to read-only (0400) and remove wal/shm
	for _, dbFileName := range []string{"memory.db", "map_graph.db"} {
		dbPath := filepath.Join(targetDir, dbFileName)
		_ = os.Remove(dbPath + "-wal")
		_ = os.Remove(dbPath + "-shm")
		if _, statErr := os.Stat(dbPath); statErr == nil {
			if chmodErr := os.Chmod(dbPath, 0400); chmodErr != nil {
				log.Printf("[WARN] agent %s failed setting read-only mode on %s: %v", req.AgentID, dbPath, chmodErr)
			}
		}
	}
	memDBPath, err := r.GraveyardMemoryDBPath(req.AgentID, agentUUID, worldTick)
	if err == nil {
		result.MemoryDBPath = memDBPath
	}

	// Remove ephemeral inventory cache if present in targetDir
	_ = os.Remove(filepath.Join(targetDir, "inventory_cache.json"))

	// Clean up ephemeral runtime state in XDG_STATE_HOME
	_ = os.Remove(r.AgentSocketPath(req.AgentID))
	_ = os.Remove(filepath.Join(r.AgentStateDir(req.AgentID), "status.json"))
	_ = os.Remove(filepath.Join(r.AgentStateDir(req.AgentID), "inventory_cache.json"))

	return result, nil
}

// copyDir recursively copies a directory tree from src to dst.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(dst, relPath)
		if info.IsDir() {
			return os.MkdirAll(targetPath, info.Mode().Perm())
		}
		return copyFile(path, targetPath, info.Mode().Perm())
	})
}

// copyFile copies a single file from src to dst with the designated permissions.
func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// isBattleDeath determines if a death occurred in combat against mobs or players.
func isBattleDeath(deathEv *civquestv1.DeathEvent) bool {
	if deathEv == nil {
		return false
	}
	if deathEv.GetKillerDisplayName() != "" || deathEv.GetKillerUuid() != "" || deathEv.GetKillerEntityId() > 0 {
		return true
	}
	src := strings.ToLower(deathEv.GetDamageSource())
	combatKeywords := []string{
		"mob", "player", "attack", "arrow", "trident", "explosion",
		"creeper", "zombie", "skeleton", "spider", "witch", "warden",
		"wither", "dragon", "blaze", "ghast", "pillager", "vindicator",
		"evoker", "ravager", "slime", "magma", "phantom", "sting",
		"magic", "fireball", "sonic", "combat",
	}
	for _, kw := range combatKeywords {
		if strings.Contains(src, kw) {
			return true
		}
	}
	return false
}
