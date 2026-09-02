package workflows

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db     *pgxpool.Pool
	logger *slog.Logger
}

func NewRepository(db *pgxpool.Pool, logger *slog.Logger) *Repository {
	return &Repository{db: db, logger: logger}
}

func newID(prefix string) string {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(data)
}

const columns = `
	w.id, w.name, w.description, w.icon, COALESCE(w.owner_user_id, ''),
	COALESCE(u.name, ''), w.owner_workspace_id, w.visibility, w.graph,
	w.created_at, w.updated_at`

// visibleSQL is the one place that decides who may see a workflow: everyone in
// the workspace sees the shared ones, and only the author sees their private
// ones. Every read goes through it so a listing and a fetch cannot disagree.
const visibleSQL = `w.owner_workspace_id = $2 AND (w.visibility = 'workspace' OR w.owner_user_id = $1)`

func scan(row pgx.Row, userID string) (Workflow, error) {
	var item Workflow
	var graphRaw []byte
	if err := row.Scan(&item.ID, &item.Name, &item.Description, &item.Icon,
		&item.OwnerUserID, &item.OwnerName, &item.WorkspaceID, &item.Visibility,
		&graphRaw, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return Workflow{}, err
	}
	item.Graph = Graph{Nodes: []Node{}, Edges: []Edge{}}
	if len(graphRaw) > 0 {
		_ = json.Unmarshal(graphRaw, &item.Graph)
	}
	if item.Graph.Nodes == nil {
		item.Graph.Nodes = []Node{}
	}
	if item.Graph.Edges == nil {
		item.Graph.Edges = []Edge{}
	}
	item.IsEditable = item.OwnerUserID == userID
	return item, nil
}

func (repository *Repository) List(ctx context.Context, userID, workspaceID string) ([]Workflow, error) {
	rows, err := repository.db.Query(ctx, `
		SELECT `+columns+`
		FROM workflows w LEFT JOIN users u ON u.id = w.owner_user_id
		WHERE `+visibleSQL+`
		ORDER BY w.updated_at DESC`, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Workflow{}
	for rows.Next() {
		item, err := scan(rows, userID)
		if err != nil {
			return nil, err
		}
		list = append(list, item)
	}
	return list, rows.Err()
}

func (repository *Repository) Get(ctx context.Context, id, userID, workspaceID string) (Workflow, error) {
	row := repository.db.QueryRow(ctx, `
		SELECT `+columns+`
		FROM workflows w LEFT JOIN users u ON u.id = w.owner_user_id
		WHERE w.id = $3 AND `+visibleSQL, userID, workspaceID, id)
	item, err := scan(row, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workflow{}, ErrNotFound
	}
	return item, err
}

// Create starts a workflow with the one node every workflow has. An empty
// canvas is a worse starting point than a Start node: the reader would have to
// know that a start is required before the editor would let them save.
func (repository *Repository) Create(ctx context.Context, userID, workspaceID, rawName, rawDescription, icon, visibility string) (Workflow, error) {
	name, err := ValidateName(rawName)
	if err != nil {
		return Workflow{}, err
	}
	description, err := ValidateDescription(rawDescription)
	if err != nil {
		return Workflow{}, err
	}
	if visibility != "workspace" {
		visibility = "private"
	}

	graph := Graph{
		Nodes: []Node{{ID: "start", Kind: KindStart, Name: "Bắt đầu", X: 80, Y: 160, Config: map[string]any{}}},
		Edges: []Edge{},
	}
	graphJSON, _ := json.Marshal(graph)

	id := newID("wfl_")
	if _, err := repository.db.Exec(ctx, `
		INSERT INTO workflows (id, name, description, icon, owner_user_id, owner_workspace_id, visibility, graph)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, name, description, icon, userID, workspaceID, visibility, graphJSON); err != nil {
		return Workflow{}, err
	}
	return repository.Get(ctx, id, userID, workspaceID)
}

// SaveGraph replaces the whole graph. The editor holds the document; there is
// nothing to merge, and a partial save would leave edges pointing at nodes
// that were never written.
func (repository *Repository) SaveGraph(ctx context.Context, id, userID, workspaceID string, graph Graph) (Workflow, error) {
	current, err := repository.Get(ctx, id, userID, workspaceID)
	if err != nil {
		return Workflow{}, err
	}
	if !current.IsEditable {
		return Workflow{}, ErrNotFound
	}
	cleaned, err := CleanGraph(graph)
	if err != nil {
		return Workflow{}, err
	}
	graphJSON, _ := json.Marshal(cleaned)
	if _, err := repository.db.Exec(ctx,
		`UPDATE workflows SET graph = $2, updated_at = NOW() WHERE id = $1`, id, graphJSON); err != nil {
		return Workflow{}, err
	}
	return repository.Get(ctx, id, userID, workspaceID)
}

type Changes struct {
	Name        *string
	Description *string
	Icon        *string
	Visibility  *string
}

func (repository *Repository) Update(ctx context.Context, id, userID, workspaceID string, changes Changes) (Workflow, error) {
	current, err := repository.Get(ctx, id, userID, workspaceID)
	if err != nil {
		return Workflow{}, err
	}
	if !current.IsEditable {
		return Workflow{}, ErrNotFound
	}

	name := current.Name
	if changes.Name != nil {
		if name, err = ValidateName(*changes.Name); err != nil {
			return Workflow{}, err
		}
	}
	description := current.Description
	if changes.Description != nil {
		if description, err = ValidateDescription(*changes.Description); err != nil {
			return Workflow{}, err
		}
	}
	icon := current.Icon
	if changes.Icon != nil {
		icon = *changes.Icon
	}
	visibility := current.Visibility
	if changes.Visibility != nil && (*changes.Visibility == "workspace" || *changes.Visibility == "private") {
		visibility = *changes.Visibility
	}

	if _, err := repository.db.Exec(ctx, `
		UPDATE workflows SET name = $2, description = $3, icon = $4, visibility = $5, updated_at = NOW()
		WHERE id = $1`, id, name, description, icon, visibility); err != nil {
		return Workflow{}, err
	}
	return repository.Get(ctx, id, userID, workspaceID)
}

func (repository *Repository) Delete(ctx context.Context, id, userID, workspaceID string) error {
	current, err := repository.Get(ctx, id, userID, workspaceID)
	if err != nil {
		return err
	}
	if !current.IsEditable {
		return ErrNotFound
	}
	_, err = repository.db.Exec(ctx, `DELETE FROM workflows WHERE id = $1`, id)
	return err
}
