package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ---------- BenchmarkPreset ----------

const bpCols = `id, name, description, tool, params_json, created_at, updated_at`

func scanBenchmarkPreset(row interface{ Scan(...any) error }) (*BenchmarkPreset, error) {
	p := &BenchmarkPreset{}
	var desc sql.NullString
	var createdAt, updatedAt string
	err := row.Scan(&p.ID, &p.Name, &desc, &p.Tool, &p.ParamsJSON, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	if desc.Valid {
		p.Description = desc.String
	}
	if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		p.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
		p.UpdatedAt = t
	}
	return p, nil
}

func (db *DB) ListBenchmarkPresets(ctx context.Context) ([]*BenchmarkPreset, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+bpCols+` FROM benchmark_presets ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*BenchmarkPreset
	for rows.Next() {
		p, err := scanBenchmarkPreset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (db *DB) FindBenchmarkPreset(ctx context.Context, id int64) (*BenchmarkPreset, error) {
	row := db.QueryRowContext(ctx, `SELECT `+bpCols+` FROM benchmark_presets WHERE id=?`, id)
	p, err := scanBenchmarkPreset(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

func (db *DB) CreateBenchmarkPreset(ctx context.Context, p *BenchmarkPreset) (*BenchmarkPreset, error) {
	if p.Name == "" || p.Tool == "" || p.ParamsJSON == "" {
		return nil, fmt.Errorf("name, tool, paramsJson required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.ExecContext(ctx, `INSERT INTO benchmark_presets
		(name, description, tool, params_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		p.Name, p.Description, p.Tool, p.ParamsJSON, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return db.FindBenchmarkPreset(ctx, id)
}

func (db *DB) UpdateBenchmarkPreset(ctx context.Context, id int64, p *BenchmarkPreset) (*BenchmarkPreset, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.ExecContext(ctx, `UPDATE benchmark_presets
		SET name=?, description=?, tool=?, params_json=?, updated_at=? WHERE id=?`,
		p.Name, p.Description, p.Tool, p.ParamsJSON, now, id)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}
	return db.FindBenchmarkPreset(ctx, id)
}

func (db *DB) DeleteBenchmarkPreset(ctx context.Context, id int64) error {
	res, err := db.ExecContext(ctx, `DELETE FROM benchmark_presets WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- IOTestPreset ----------

const ipCols = `id, name, description, category, config_json, created_at, updated_at`

func scanIOTestPreset(row interface{ Scan(...any) error }) (*IOTestPreset, error) {
	p := &IOTestPreset{}
	var desc sql.NullString
	var createdAt, updatedAt string
	err := row.Scan(&p.ID, &p.Name, &desc, &p.Category, &p.ConfigJSON, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	if desc.Valid {
		p.Description = desc.String
	}
	if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		p.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
		p.UpdatedAt = t
	}
	return p, nil
}

func (db *DB) ListIOTestPresets(ctx context.Context) ([]*IOTestPreset, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+ipCols+` FROM iotest_presets ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*IOTestPreset
	for rows.Next() {
		p, err := scanIOTestPreset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (db *DB) CreateIOTestPreset(ctx context.Context, p *IOTestPreset) (*IOTestPreset, error) {
	if p.Name == "" || p.Category == "" || p.ConfigJSON == "" {
		return nil, fmt.Errorf("name, category, configJson required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.ExecContext(ctx, `INSERT INTO iotest_presets
		(name, description, category, config_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		p.Name, p.Description, p.Category, p.ConfigJSON, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	row := db.QueryRowContext(ctx, `SELECT `+ipCols+` FROM iotest_presets WHERE id=?`, id)
	return scanIOTestPreset(row)
}

func (db *DB) UpdateIOTestPreset(ctx context.Context, id int64, p *IOTestPreset) (*IOTestPreset, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.ExecContext(ctx, `UPDATE iotest_presets
		SET name=?, description=?, category=?, config_json=?, updated_at=? WHERE id=?`,
		p.Name, p.Description, p.Category, p.ConfigJSON, now, id)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}
	row := db.QueryRowContext(ctx, `SELECT `+ipCols+` FROM iotest_presets WHERE id=?`, id)
	return scanIOTestPreset(row)
}

func (db *DB) DeleteIOTestPreset(ctx context.Context, id int64) error {
	res, err := db.ExecContext(ctx, `DELETE FROM iotest_presets WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- ScenarioTemplate ----------

const stCols = `id, name, description, repeat_count, steps_json, loops_json, created_at, updated_at`

func scanScenarioTemplate(row interface{ Scan(...any) error }) (*ScenarioTemplate, error) {
	t := &ScenarioTemplate{}
	var desc sql.NullString
	var createdAt, updatedAt string
	err := row.Scan(&t.ID, &t.Name, &desc, &t.RepeatCount, &t.StepsJSON, &t.LoopsJSON, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	if desc.Valid {
		t.Description = desc.String
	}
	if ct, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		t.CreatedAt = ct
	}
	if ut, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
		t.UpdatedAt = ut
	}
	return t, nil
}

func (db *DB) ListScenarioTemplates(ctx context.Context) ([]*ScenarioTemplate, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+stCols+` FROM scenario_templates ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ScenarioTemplate
	for rows.Next() {
		t, err := scanScenarioTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (db *DB) FindScenarioTemplate(ctx context.Context, id int64) (*ScenarioTemplate, error) {
	row := db.QueryRowContext(ctx, `SELECT `+stCols+` FROM scenario_templates WHERE id=?`, id)
	t, err := scanScenarioTemplate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

func (db *DB) CreateScenarioTemplate(ctx context.Context, t *ScenarioTemplate) (*ScenarioTemplate, error) {
	if t.Name == "" || t.StepsJSON == "" {
		return nil, fmt.Errorf("name and stepsJson required")
	}
	if t.RepeatCount == 0 {
		t.RepeatCount = 1
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.ExecContext(ctx, `INSERT INTO scenario_templates
		(name, description, repeat_count, steps_json, loops_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.Name, t.Description, t.RepeatCount, t.StepsJSON, t.LoopsJSON, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return db.FindScenarioTemplate(ctx, id)
}

func (db *DB) UpdateScenarioTemplate(ctx context.Context, id int64, t *ScenarioTemplate) (*ScenarioTemplate, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.ExecContext(ctx, `UPDATE scenario_templates
		SET name=?, description=?, repeat_count=?, steps_json=?, loops_json=?, updated_at=? WHERE id=?`,
		t.Name, t.Description, t.RepeatCount, t.StepsJSON, t.LoopsJSON, now, id)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}
	return db.FindScenarioTemplate(ctx, id)
}

func (db *DB) DeleteScenarioTemplate(ctx context.Context, id int64) error {
	res, err := db.ExecContext(ctx, `DELETE FROM scenario_templates WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
