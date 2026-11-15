package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Governance Regime Types per ADR-016.
const (
	RegimeTypeDemocracy          = "DEMOCRACY"
	RegimeTypeBenevolentMonarchy = "BENEVOLENT_MONARCHY"
	RegimeTypeWarlord            = "WARLORD"
	RegimeTypeOligarchy          = "OLIGARCHY"
)

// IsValidRegimeType checks whether regime is a recognized governance regime type.
func IsValidRegimeType(regime string) bool {
	switch regime {
	case RegimeTypeDemocracy, RegimeTypeBenevolentMonarchy, RegimeTypeWarlord, RegimeTypeOligarchy:
		return true
	default:
		return false
	}
}

// Leader Types.
const (
	LeaderTypeAgent  = "AGENT"
	LeaderTypePlayer = "PLAYER"
)

// IsValidLeaderType checks whether lType is a recognized leader type.
func IsValidLeaderType(lType string) bool {
	switch lType {
	case LeaderTypeAgent, LeaderTypePlayer:
		return true
	default:
		return false
	}
}

// Civic Assembly Statuses.
const (
	AssemblyStatusActive    = "ACTIVE"
	AssemblyStatusConcluded = "CONCLUDED"
	AssemblyStatusAbandoned = "ABANDONED"
)

// IsValidAssemblyStatus checks whether status is a recognized civic assembly status.
func IsValidAssemblyStatus(status string) bool {
	switch status {
	case AssemblyStatusActive, AssemblyStatusConcluded, AssemblyStatusAbandoned:
		return true
	default:
		return false
	}
}

// Trigger Axes per ADR-016 ("Axes of Change").
const (
	TriggerAxisMaterialShock  = "MATERIAL_SHOCK"
	TriggerAxisSecurityCrisis = "SECURITY_CRISIS"
	TriggerAxisSuccession     = "DYNASTIC_SUCCESSION"
	TriggerAxisGoalVacuum     = "GOAL_VACUUM"
)

// Voter Types.
const (
	VoterTypeAgent  = "AGENT"
	VoterTypePlayer = "PLAYER"
)

// IsValidVoterType checks whether vType is a recognized voter type.
func IsValidVoterType(vType string) bool {
	switch vType {
	case VoterTypeAgent, VoterTypePlayer:
		return true
	default:
		return false
	}
}

// Civic Edict Statuses.
const (
	EdictStatusActive    = "ACTIVE"
	EdictStatusCompleted = "COMPLETED"
	EdictStatusRescinded = "RESCINDED"
)

// IsValidEdictStatus checks whether status is a recognized edict status.
func IsValidEdictStatus(status string) bool {
	switch status {
	case EdictStatusActive, EdictStatusCompleted, EdictStatusRescinded:
		return true
	default:
		return false
	}
}

// Sentinel errors for governance operations.
var (
	ErrGovernanceNotFound     = errors.New("clan governance not found")
	ErrAssemblyNotFound       = errors.New("civic assembly not found")
	ErrAssemblyNotActive      = errors.New("civic assembly is not active")
	ErrVoteNotFound           = errors.New("civic vote not found")
	ErrEdictNotFound          = errors.New("civic edict not found")
	ErrInvalidRegimeType      = errors.New("invalid governance regime type")
	ErrInvalidLeaderType      = errors.New("invalid leader type")
	ErrInvalidAssemblyStatus  = errors.New("invalid assembly status")
	ErrInvalidVoterType       = errors.New("invalid voter type")
	ErrInvalidEdictStatus     = errors.New("invalid edict status")
	ErrInvalidLegitimacyScore = errors.New("legitimacy score must be between 0.0 and 100.0")
)

// -----------------------------------------------------------------------------
// Clan Governance CRUD
// -----------------------------------------------------------------------------

// GetClanGovernance retrieves the current governance configuration for the clan.
func (c *CivicRegistryDB) GetClanGovernance() (*ClanGovernance, error) {
	return c.GetClanGovernanceContext(context.Background())
}

// GetClanGovernanceContext retrieves the governance configuration for the clan with context.
func (c *CivicRegistryDB) GetClanGovernanceContext(ctx context.Context) (*ClanGovernance, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT clan_id, regime_type, leader_agent_id, leader_type, mandate_goal,
		       legitimacy_score, term_start_tick, last_election_tick, updated_at
		FROM clan_governance
		WHERE clan_id = ?;
	`

	var gov ClanGovernance
	var updatedStr string
	err := c.db.QueryRowContext(ctx, query, c.clanID).Scan(
		&gov.ClanID,
		&gov.RegimeType,
		&gov.LeaderAgentID,
		&gov.LeaderType,
		&gov.MandateGoal,
		&gov.LegitimacyScore,
		&gov.TermStartTick,
		&gov.LastElectionTick,
		&updatedStr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrGovernanceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying clan governance: %w", err)
	}

	gov.UpdatedAt = parseDBTimestamp(updatedStr)
	return &gov, nil
}

// SetClanGovernance creates or updates the governance configuration for the clan.
func (c *CivicRegistryDB) SetClanGovernance(gov *ClanGovernance) error {
	return c.SetClanGovernanceContext(context.Background(), gov)
}

// SetClanGovernanceContext creates or updates the governance configuration for the clan with context.
func (c *CivicRegistryDB) SetClanGovernanceContext(ctx context.Context, gov *ClanGovernance) error {
	if gov == nil {
		return errors.New("governance record cannot be nil")
	}
	if gov.ClanID == "" {
		gov.ClanID = c.clanID
	}
	if gov.LeaderType == "" {
		gov.LeaderType = LeaderTypeAgent
	}
	if !IsValidRegimeType(gov.RegimeType) {
		return fmt.Errorf("%w: %q", ErrInvalidRegimeType, gov.RegimeType)
	}
	if !IsValidLeaderType(gov.LeaderType) {
		return fmt.Errorf("%w: %q", ErrInvalidLeaderType, gov.LeaderType)
	}
	if gov.LegitimacyScore < 0.0 || gov.LegitimacyScore > 100.0 {
		return ErrInvalidLegitimacyScore
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	query := `
		INSERT INTO clan_governance (
			clan_id, regime_type, leader_agent_id, leader_type, mandate_goal,
			legitimacy_score, term_start_tick, last_election_tick, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(clan_id) DO UPDATE SET
			regime_type = excluded.regime_type,
			leader_agent_id = excluded.leader_agent_id,
			leader_type = excluded.leader_type,
			mandate_goal = excluded.mandate_goal,
			legitimacy_score = excluded.legitimacy_score,
			term_start_tick = excluded.term_start_tick,
			last_election_tick = excluded.last_election_tick,
			updated_at = CURRENT_TIMESTAMP;
	`

	_, err := c.db.ExecContext(ctx, query,
		gov.ClanID,
		gov.RegimeType,
		gov.LeaderAgentID,
		gov.LeaderType,
		gov.MandateGoal,
		gov.LegitimacyScore,
		gov.TermStartTick,
		gov.LastElectionTick,
	)
	if err != nil {
		return fmt.Errorf("setting clan governance: %w", err)
	}

	return nil
}

// UpdateLegitimacyScore updates the clan's current legitimacy score.
func (c *CivicRegistryDB) UpdateLegitimacyScore(score float64) error {
	return c.UpdateLegitimacyScoreContext(context.Background(), score)
}

// UpdateLegitimacyScoreContext updates the clan's current legitimacy score with context.
func (c *CivicRegistryDB) UpdateLegitimacyScoreContext(ctx context.Context, score float64) error {
	if score < 0.0 || score > 100.0 {
		return ErrInvalidLegitimacyScore
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	res, err := c.db.ExecContext(ctx, `
		UPDATE clan_governance
		SET legitimacy_score = ?, updated_at = CURRENT_TIMESTAMP
		WHERE clan_id = ?;
	`, score, c.clanID)
	if err != nil {
		return fmt.Errorf("updating legitimacy score: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrGovernanceNotFound
	}

	return nil
}

// -----------------------------------------------------------------------------
// Civic Assemblies CRUD
// -----------------------------------------------------------------------------

// CreateAssembly initiates a new civic assembly.
func (c *CivicRegistryDB) CreateAssembly(assembly *CivicAssembly) error {
	return c.CreateAssemblyContext(context.Background(), assembly)
}

// CreateAssemblyContext initiates a new civic assembly with context.
func (c *CivicRegistryDB) CreateAssemblyContext(ctx context.Context, assembly *CivicAssembly) error {
	if assembly == nil {
		return errors.New("assembly cannot be nil")
	}
	if assembly.ID == "" {
		return errors.New("assembly id cannot be empty")
	}
	if assembly.ClanID == "" {
		assembly.ClanID = c.clanID
	}
	if assembly.TriggerAxis == "" {
		return errors.New("trigger axis cannot be empty")
	}
	if assembly.Status == "" {
		assembly.Status = AssemblyStatusActive
	}
	if !IsValidAssemblyStatus(assembly.Status) {
		return fmt.Errorf("%w: %q", ErrInvalidAssemblyStatus, assembly.Status)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	query := `
		INSERT INTO civic_assemblies (
			id, clan_id, trigger_axis, status, winning_proposal_id, opened_tick, closed_tick, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP);
	`

	_, err := c.db.ExecContext(ctx, query,
		assembly.ID,
		assembly.ClanID,
		assembly.TriggerAxis,
		assembly.Status,
		assembly.WinningProposalID,
		assembly.OpenedTick,
		assembly.ClosedTick,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("%w: assembly %s", ErrDuplicateRecord, assembly.ID)
		}
		return fmt.Errorf("creating civic assembly: %w", err)
	}

	return nil
}

// GetAssembly retrieves an assembly by its unique ID.
func (c *CivicRegistryDB) GetAssembly(id string) (*CivicAssembly, error) {
	return c.GetAssemblyContext(context.Background(), id)
}

// GetAssemblyContext retrieves an assembly by its unique ID with context.
func (c *CivicRegistryDB) GetAssemblyContext(ctx context.Context, id string) (*CivicAssembly, error) {
	if id == "" {
		return nil, errors.New("assembly id cannot be empty")
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT id, clan_id, trigger_axis, status, winning_proposal_id, opened_tick, closed_tick, created_at
		FROM civic_assemblies
		WHERE id = ?;
	`

	var a CivicAssembly
	var createdStr string
	err := c.db.QueryRowContext(ctx, query, id).Scan(
		&a.ID,
		&a.ClanID,
		&a.TriggerAxis,
		&a.Status,
		&a.WinningProposalID,
		&a.OpenedTick,
		&a.ClosedTick,
		&createdStr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAssemblyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying civic assembly %s: %w", id, err)
	}

	a.CreatedAt = parseDBTimestamp(createdStr)
	return &a, nil
}

// GetActiveAssembly retrieves the current active assembly for the clan, if any.
func (c *CivicRegistryDB) GetActiveAssembly() (*CivicAssembly, error) {
	return c.GetActiveAssemblyContext(context.Background())
}

// GetActiveAssemblyContext retrieves the current active assembly for the clan with context.
func (c *CivicRegistryDB) GetActiveAssemblyContext(ctx context.Context) (*CivicAssembly, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT id, clan_id, trigger_axis, status, winning_proposal_id, opened_tick, closed_tick, created_at
		FROM civic_assemblies
		WHERE clan_id = ? AND status = ?
		ORDER BY opened_tick DESC, created_at DESC
		LIMIT 1;
	`

	var a CivicAssembly
	var createdStr string
	err := c.db.QueryRowContext(ctx, query, c.clanID, AssemblyStatusActive).Scan(
		&a.ID,
		&a.ClanID,
		&a.TriggerAxis,
		&a.Status,
		&a.WinningProposalID,
		&a.OpenedTick,
		&a.ClosedTick,
		&createdStr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying active civic assembly: %w", err)
	}

	a.CreatedAt = parseDBTimestamp(createdStr)
	return &a, nil
}

// ListAssemblies lists recorded assemblies for the clan, ordered newest first.
func (c *CivicRegistryDB) ListAssemblies(limit int) ([]*CivicAssembly, error) {
	return c.ListAssembliesContext(context.Background(), limit)
}

// ListAssembliesContext lists recorded assemblies for the clan with context.
func (c *CivicRegistryDB) ListAssembliesContext(ctx context.Context, limit int) ([]*CivicAssembly, error) {
	if limit <= 0 {
		limit = 100
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT id, clan_id, trigger_axis, status, winning_proposal_id, opened_tick, closed_tick, created_at
		FROM civic_assemblies
		WHERE clan_id = ?
		ORDER BY opened_tick DESC, created_at DESC
		LIMIT ?;
	`

	rows, err := c.db.QueryContext(ctx, query, c.clanID, limit)
	if err != nil {
		return nil, fmt.Errorf("querying civic assemblies: %w", err)
	}
	defer rows.Close()

	var assemblies []*CivicAssembly
	for rows.Next() {
		var a CivicAssembly
		var createdStr string
		if err := rows.Scan(
			&a.ID,
			&a.ClanID,
			&a.TriggerAxis,
			&a.Status,
			&a.WinningProposalID,
			&a.OpenedTick,
			&a.ClosedTick,
			&createdStr,
		); err != nil {
			return nil, fmt.Errorf("scanning civic assembly row: %w", err)
		}
		a.CreatedAt = parseDBTimestamp(createdStr)
		assemblies = append(assemblies, &a)
	}

	return assemblies, rows.Err()
}

// ConcludeAssembly closes an active assembly with a winning proposal ID.
func (c *CivicRegistryDB) ConcludeAssembly(id, winningProposalID string, closedTick int64) error {
	return c.ConcludeAssemblyContext(context.Background(), id, winningProposalID, closedTick)
}

// ConcludeAssemblyContext closes an active assembly with context.
func (c *CivicRegistryDB) ConcludeAssemblyContext(ctx context.Context, id, winningProposalID string, closedTick int64) error {
	if id == "" {
		return errors.New("assembly id cannot be empty")
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	res, err := c.db.ExecContext(ctx, `
		UPDATE civic_assemblies
		SET status = ?, winning_proposal_id = ?, closed_tick = ?
		WHERE id = ?;
	`, AssemblyStatusConcluded, winningProposalID, closedTick, id)
	if err != nil {
		return fmt.Errorf("concluding civic assembly %s: %w", id, err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrAssemblyNotFound
	}

	return nil
}

// AbandonAssembly terminates an active assembly without enacting a proposal.
func (c *CivicRegistryDB) AbandonAssembly(id string, closedTick int64) error {
	return c.AbandonAssemblyContext(context.Background(), id, closedTick)
}

// AbandonAssemblyContext terminates an active assembly without enacting a proposal with context.
func (c *CivicRegistryDB) AbandonAssemblyContext(ctx context.Context, id string, closedTick int64) error {
	if id == "" {
		return errors.New("assembly id cannot be empty")
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	res, err := c.db.ExecContext(ctx, `
		UPDATE civic_assemblies
		SET status = ?, closed_tick = ?
		WHERE id = ?;
	`, AssemblyStatusAbandoned, closedTick, id)
	if err != nil {
		return fmt.Errorf("abandoning civic assembly %s: %w", id, err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrAssemblyNotFound
	}

	return nil
}

// -----------------------------------------------------------------------------
// Civic Votes CRUD & Ballot Tallying
// -----------------------------------------------------------------------------

// RecordVote casts or updates a settler or player ballot in an active assembly.
func (c *CivicRegistryDB) RecordVote(vote *CivicVote) error {
	return c.RecordVoteContext(context.Background(), vote)
}

// RecordVoteContext casts or updates a settler or player ballot with context.
func (c *CivicRegistryDB) RecordVoteContext(ctx context.Context, vote *CivicVote) error {
	if vote == nil {
		return errors.New("vote cannot be nil")
	}
	if vote.AssemblyID == "" {
		return errors.New("assembly id cannot be empty")
	}
	if vote.AgentID == "" {
		return errors.New("agent id cannot be empty")
	}
	if vote.ProposalID == "" {
		return errors.New("proposal id cannot be empty")
	}
	if vote.VoterType == "" {
		vote.VoterType = VoterTypeAgent
	}
	if !IsValidVoterType(vote.VoterType) {
		return fmt.Errorf("%w: %q", ErrInvalidVoterType, vote.VoterType)
	}
	if vote.VoteWeight < 0.0 {
		return errors.New("vote weight must be non-negative")
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning vote transaction: %w", err)
	}
	defer tx.Rollback()

	var status string
	err = tx.QueryRowContext(ctx, `SELECT status FROM civic_assemblies WHERE id = ?;`, vote.AssemblyID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAssemblyNotFound
	}
	if err != nil {
		return fmt.Errorf("verifying assembly status: %w", err)
	}
	if status != AssemblyStatusActive {
		return fmt.Errorf("%w: assembly %s is %s", ErrAssemblyNotActive, vote.AssemblyID, status)
	}

	query := `
		INSERT INTO civic_votes (
			assembly_id, agent_id, voter_type, proposal_id, vote_weight, rationale, world_tick, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(assembly_id, agent_id) DO UPDATE SET
			voter_type = excluded.voter_type,
			proposal_id = excluded.proposal_id,
			vote_weight = excluded.vote_weight,
			rationale = excluded.rationale,
			world_tick = excluded.world_tick,
			created_at = CURRENT_TIMESTAMP;
	`

	if _, err := tx.ExecContext(ctx, query,
		vote.AssemblyID,
		vote.AgentID,
		vote.VoterType,
		vote.ProposalID,
		vote.VoteWeight,
		vote.Rationale,
		vote.WorldTick,
	); err != nil {
		return fmt.Errorf("recording vote: %w", err)
	}

	return tx.Commit()
}

// GetAssemblyVotes retrieves all recorded ballots cast in an assembly.
func (c *CivicRegistryDB) GetAssemblyVotes(assemblyID string) ([]*CivicVote, error) {
	return c.GetAssemblyVotesContext(context.Background(), assemblyID)
}

// GetAssemblyVotesContext retrieves all recorded ballots cast in an assembly with context.
func (c *CivicRegistryDB) GetAssemblyVotesContext(ctx context.Context, assemblyID string) ([]*CivicVote, error) {
	if assemblyID == "" {
		return nil, errors.New("assembly id cannot be empty")
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT assembly_id, agent_id, voter_type, proposal_id, vote_weight, rationale, world_tick, created_at
		FROM civic_votes
		WHERE assembly_id = ?
		ORDER BY created_at ASC;
	`

	rows, err := c.db.QueryContext(ctx, query, assemblyID)
	if err != nil {
		return nil, fmt.Errorf("querying assembly votes: %w", err)
	}
	defer rows.Close()

	var votes []*CivicVote
	for rows.Next() {
		var v CivicVote
		var createdStr string
		if err := rows.Scan(
			&v.AssemblyID,
			&v.AgentID,
			&v.VoterType,
			&v.ProposalID,
			&v.VoteWeight,
			&v.Rationale,
			&v.WorldTick,
			&createdStr,
		); err != nil {
			return nil, fmt.Errorf("scanning vote row: %w", err)
		}
		v.CreatedAt = parseDBTimestamp(createdStr)
		votes = append(votes, &v)
	}

	return votes, rows.Err()
}

// GetVote retrieves a single voter's ballot in an assembly.
func (c *CivicRegistryDB) GetVote(assemblyID, agentID string) (*CivicVote, error) {
	return c.GetVoteContext(context.Background(), assemblyID, agentID)
}

// GetVoteContext retrieves a single voter's ballot in an assembly with context.
func (c *CivicRegistryDB) GetVoteContext(ctx context.Context, assemblyID, agentID string) (*CivicVote, error) {
	if assemblyID == "" || agentID == "" {
		return nil, errors.New("assembly id and agent id cannot be empty")
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT assembly_id, agent_id, voter_type, proposal_id, vote_weight, rationale, world_tick, created_at
		FROM civic_votes
		WHERE assembly_id = ? AND agent_id = ?;
	`

	var v CivicVote
	var createdStr string
	err := c.db.QueryRowContext(ctx, query, assemblyID, agentID).Scan(
		&v.AssemblyID,
		&v.AgentID,
		&v.VoterType,
		&v.ProposalID,
		&v.VoteWeight,
		&v.Rationale,
		&v.WorldTick,
		&createdStr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrVoteNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying vote: %w", err)
	}

	v.CreatedAt = parseDBTimestamp(createdStr)
	return &v, nil
}

// TallyAssemblyVotes tallies the cumulative weighted scores and ballot counts for all proposals in an assembly.
func (c *CivicRegistryDB) TallyAssemblyVotes(assemblyID string) ([]AssemblyBallotTally, error) {
	return c.TallyAssemblyVotesContext(context.Background(), assemblyID)
}

// TallyAssemblyVotesContext tallies cumulative weighted scores and ballot counts with context.
func (c *CivicRegistryDB) TallyAssemblyVotesContext(ctx context.Context, assemblyID string) ([]AssemblyBallotTally, error) {
	if assemblyID == "" {
		return nil, errors.New("assembly id cannot be empty")
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT proposal_id, COALESCE(SUM(vote_weight), 0.0) AS total_weight, COUNT(*) AS vote_count
		FROM civic_votes
		WHERE assembly_id = ?
		GROUP BY proposal_id
		ORDER BY total_weight DESC, vote_count DESC;
	`

	rows, err := c.db.QueryContext(ctx, query, assemblyID)
	if err != nil {
		return nil, fmt.Errorf("tallying assembly votes: %w", err)
	}
	defer rows.Close()

	var tallies []AssemblyBallotTally
	for rows.Next() {
		var t AssemblyBallotTally
		if err := rows.Scan(&t.ProposalID, &t.TotalWeight, &t.VoteCount); err != nil {
			return nil, fmt.Errorf("scanning ballot tally row: %w", err)
		}
		tallies = append(tallies, t)
	}

	return tallies, rows.Err()
}

// -----------------------------------------------------------------------------
// Civic Edicts CRUD
// -----------------------------------------------------------------------------

// CreateEdict issues a new unilateral sovereign edict.
func (c *CivicRegistryDB) CreateEdict(edict *CivicEdict) error {
	return c.CreateEdictContext(context.Background(), edict)
}

// CreateEdictContext issues a new unilateral sovereign edict with context.
func (c *CivicRegistryDB) CreateEdictContext(ctx context.Context, edict *CivicEdict) error {
	if edict == nil {
		return errors.New("edict cannot be nil")
	}
	if edict.ID == "" {
		return errors.New("edict id cannot be empty")
	}
	if edict.ClanID == "" {
		edict.ClanID = c.clanID
	}
	if edict.LeaderAgentID == "" {
		return errors.New("leader agent id cannot be empty")
	}
	if edict.EdictTitle == "" {
		return errors.New("edict title cannot be empty")
	}
	if !IsValidRegimeType(edict.RegimeType) {
		return fmt.Errorf("%w: %q", ErrInvalidRegimeType, edict.RegimeType)
	}
	if edict.Status == "" {
		edict.Status = EdictStatusActive
	}
	if !IsValidEdictStatus(edict.Status) {
		return fmt.Errorf("%w: %q", ErrInvalidEdictStatus, edict.Status)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	query := `
		INSERT INTO civic_edicts (
			id, clan_id, leader_agent_id, regime_type, edict_title, blueprint_id,
			raw_decree, status, compliance_count, dissent_count, issued_tick, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP);
	`

	_, err := c.db.ExecContext(ctx, query,
		edict.ID,
		edict.ClanID,
		edict.LeaderAgentID,
		edict.RegimeType,
		edict.EdictTitle,
		edict.BlueprintID,
		edict.RawDecree,
		edict.Status,
		edict.ComplianceCount,
		edict.DissentCount,
		edict.IssuedTick,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("%w: edict %s", ErrDuplicateRecord, edict.ID)
		}
		return fmt.Errorf("creating civic edict: %w", err)
	}

	return nil
}

// GetEdict retrieves an edict by its unique ID.
func (c *CivicRegistryDB) GetEdict(id string) (*CivicEdict, error) {
	return c.GetEdictContext(context.Background(), id)
}

// GetEdictContext retrieves an edict by its unique ID with context.
func (c *CivicRegistryDB) GetEdictContext(ctx context.Context, id string) (*CivicEdict, error) {
	if id == "" {
		return nil, errors.New("edict id cannot be empty")
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	query := `
		SELECT id, clan_id, leader_agent_id, regime_type, edict_title, blueprint_id,
		       raw_decree, status, compliance_count, dissent_count, issued_tick, created_at
		FROM civic_edicts
		WHERE id = ?;
	`

	var e CivicEdict
	var createdStr string
	err := c.db.QueryRowContext(ctx, query, id).Scan(
		&e.ID,
		&e.ClanID,
		&e.LeaderAgentID,
		&e.RegimeType,
		&e.EdictTitle,
		&e.BlueprintID,
		&e.RawDecree,
		&e.Status,
		&e.ComplianceCount,
		&e.DissentCount,
		&e.IssuedTick,
		&createdStr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrEdictNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying edict %s: %w", id, err)
	}

	e.CreatedAt = parseDBTimestamp(createdStr)
	return &e, nil
}

// ListEdicts retrieves edicts for the clan, optionally filtered by status.
func (c *CivicRegistryDB) ListEdicts(statusFilter string, limit int) ([]*CivicEdict, error) {
	return c.ListEdictsContext(context.Background(), statusFilter, limit)
}

// ListEdictsContext retrieves edicts for the clan with context.
func (c *CivicRegistryDB) ListEdictsContext(ctx context.Context, statusFilter string, limit int) ([]*CivicEdict, error) {
	if limit <= 0 {
		limit = 100
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.isClosed {
		return nil, ErrClosed
	}

	var rows *sql.Rows
	var err error

	if statusFilter != "" {
		query := `
			SELECT id, clan_id, leader_agent_id, regime_type, edict_title, blueprint_id,
			       raw_decree, status, compliance_count, dissent_count, issued_tick, created_at
			FROM civic_edicts
			WHERE clan_id = ? AND status = ?
			ORDER BY issued_tick DESC, created_at DESC
			LIMIT ?;
		`
		rows, err = c.db.QueryContext(ctx, query, c.clanID, statusFilter, limit)
	} else {
		query := `
			SELECT id, clan_id, leader_agent_id, regime_type, edict_title, blueprint_id,
			       raw_decree, status, compliance_count, dissent_count, issued_tick, created_at
			FROM civic_edicts
			WHERE clan_id = ?
			ORDER BY issued_tick DESC, created_at DESC
			LIMIT ?;
		`
		rows, err = c.db.QueryContext(ctx, query, c.clanID, limit)
	}

	if err != nil {
		return nil, fmt.Errorf("querying civic edicts: %w", err)
	}
	defer rows.Close()

	var edicts []*CivicEdict
	for rows.Next() {
		var e CivicEdict
		var createdStr string
		if err := rows.Scan(
			&e.ID,
			&e.ClanID,
			&e.LeaderAgentID,
			&e.RegimeType,
			&e.EdictTitle,
			&e.BlueprintID,
			&e.RawDecree,
			&e.Status,
			&e.ComplianceCount,
			&e.DissentCount,
			&e.IssuedTick,
			&createdStr,
		); err != nil {
			return nil, fmt.Errorf("scanning civic edict row: %w", err)
		}
		e.CreatedAt = parseDBTimestamp(createdStr)
		edicts = append(edicts, &e)
	}

	return edicts, rows.Err()
}

// UpdateEdictStatus updates the lifecycle status of an edict.
func (c *CivicRegistryDB) UpdateEdictStatus(id, newStatus string) error {
	return c.UpdateEdictStatusContext(context.Background(), id, newStatus)
}

// UpdateEdictStatusContext updates the lifecycle status of an edict with context.
func (c *CivicRegistryDB) UpdateEdictStatusContext(ctx context.Context, id, newStatus string) error {
	if id == "" {
		return errors.New("edict id cannot be empty")
	}
	if !IsValidEdictStatus(newStatus) {
		return fmt.Errorf("%w: %q", ErrInvalidEdictStatus, newStatus)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	res, err := c.db.ExecContext(ctx, `
		UPDATE civic_edicts
		SET status = ?
		WHERE id = ?;
	`, newStatus, id)
	if err != nil {
		return fmt.Errorf("updating edict status: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrEdictNotFound
	}

	return nil
}

// RecordEdictCompliance records settler compliance or dissent for an edict.
func (c *CivicRegistryDB) RecordEdictCompliance(id string, compliant bool) error {
	return c.RecordEdictComplianceContext(context.Background(), id, compliant)
}

// RecordEdictComplianceContext records settler compliance or dissent for an edict with context.
func (c *CivicRegistryDB) RecordEdictComplianceContext(ctx context.Context, id string, compliant bool) error {
	if id == "" {
		return errors.New("edict id cannot be empty")
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	var query string
	if compliant {
		query = `UPDATE civic_edicts SET compliance_count = compliance_count + 1 WHERE id = ?;`
	} else {
		query = `UPDATE civic_edicts SET dissent_count = dissent_count + 1 WHERE id = ?;`
	}

	res, err := c.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("recording edict compliance: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrEdictNotFound
	}

	return nil
}

// UpdateEdictCompliance adjusts compliance and dissent counts with arbitrary deltas.
func (c *CivicRegistryDB) UpdateEdictCompliance(id string, complianceDelta, dissentDelta int) error {
	return c.UpdateEdictComplianceContext(context.Background(), id, complianceDelta, dissentDelta)
}

// UpdateEdictComplianceContext adjusts compliance and dissent counts with context.
func (c *CivicRegistryDB) UpdateEdictComplianceContext(ctx context.Context, id string, complianceDelta, dissentDelta int) error {
	if id == "" {
		return errors.New("edict id cannot be empty")
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	if c.isClosed {
		c.mu.RUnlock()
		return ErrClosed
	}
	c.mu.RUnlock()

	query := `
		UPDATE civic_edicts
		SET compliance_count = MAX(0, compliance_count + ?),
		    dissent_count = MAX(0, dissent_count + ?)
		WHERE id = ?;
	`

	res, err := c.db.ExecContext(ctx, query, complianceDelta, dissentDelta, id)
	if err != nil {
		return fmt.Errorf("updating edict compliance: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrEdictNotFound
	}

	return nil
}
