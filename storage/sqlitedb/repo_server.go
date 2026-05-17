package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrNotFound = errors.New("not found")

const serverCols = `id, name, host, port, enabled, description, created_at, updated_at`

func scanServer(row interface{ Scan(...any) error }) (*AgentServer, error) {
	s := &AgentServer{}
	var enabled int
	var desc sql.NullString
	var createdAt, updatedAt string
	err := row.Scan(&s.ID, &s.Name, &s.Host, &s.Port, &enabled, &desc, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	s.Enabled = enabled != 0
	if desc.Valid {
		s.Description = desc.String
	}
	if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		s.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
		s.UpdatedAt = t
	}
	return s, nil
}

// ListServers — 모든 등록 서버 (id ASC).
func (db *DB) ListServers(ctx context.Context) ([]*AgentServer, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+serverCols+` FROM agent_servers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AgentServer
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (db *DB) FindServer(ctx context.Context, id int64) (*AgentServer, error) {
	row := db.QueryRowContext(ctx, `SELECT `+serverCols+` FROM agent_servers WHERE id=?`, id)
	s, err := scanServer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return s, err
}

func (db *DB) CreateServer(ctx context.Context, s *AgentServer) (*AgentServer, error) {
	if s.Name == "" || s.Host == "" {
		return nil, fmt.Errorf("name and host required")
	}
	if s.Port == 0 {
		s.Port = 50051
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	enabled := 0
	if s.Enabled {
		enabled = 1
	}
	res, err := db.ExecContext(ctx, `INSERT INTO agent_servers
		(name, host, port, enabled, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.Name, s.Host, s.Port, enabled, s.Description, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return db.FindServer(ctx, id)
}

func (db *DB) UpdateServer(ctx context.Context, id int64, s *AgentServer) (*AgentServer, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	enabled := 0
	if s.Enabled {
		enabled = 1
	}
	res, err := db.ExecContext(ctx, `UPDATE agent_servers
		SET name=?, host=?, port=?, enabled=?, description=?, updated_at=? WHERE id=?`,
		s.Name, s.Host, s.Port, enabled, s.Description, now, id)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}
	return db.FindServer(ctx, id)
}

func (db *DB) DeleteServer(ctx context.Context, id int64) error {
	res, err := db.ExecContext(ctx, `DELETE FROM agent_servers WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
