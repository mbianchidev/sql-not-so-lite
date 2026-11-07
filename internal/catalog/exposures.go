package catalog

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

type APIExposure struct {
	Alias           string   `json:"alias"`
	DatabaseID      int64    `json:"database_id"`
	ManagedDatabase string   `json:"managed_database,omitempty"`
	Tables          []string `json:"tables"`
	Writable        bool     `json:"writable"`
	TokenHash       string   `json:"-"`
}

func scanExposure(row scanner) (*APIExposure, error) {
	var exposure APIExposure
	var tables string
	if err := row.Scan(&exposure.Alias, &exposure.DatabaseID, &exposure.ManagedDatabase, &tables, &exposure.Writable, &exposure.TokenHash); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(tables), &exposure.Tables); err != nil {
		return nil, fmt.Errorf("read exposure table allowlist: %w", err)
	}
	return &exposure, nil
}

func (c *Catalog) ListAPIExposures() ([]APIExposure, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	rows, err := c.db.Query(`SELECT alias,database_id,managed_database,tables_json,writable,token_hash FROM api_exposures ORDER BY alias`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []APIExposure{}
	for rows.Next() {
		exposure, err := scanExposure(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *exposure)
	}
	return result, rows.Err()
}

func (c *Catalog) GetAPIExposure(alias string) (*APIExposure, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return scanExposure(c.db.QueryRow(`SELECT alias,database_id,managed_database,tables_json,writable,token_hash FROM api_exposures WHERE alias=?`, alias))
}

func (c *Catalog) CreateAPIExposure(exposure *APIExposure) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	tables, err := json.Marshal(exposure.Tables)
	if err != nil {
		return err
	}
	_, err = c.db.Exec(`INSERT INTO api_exposures(alias,database_id,managed_database,tables_json,writable,token_hash) VALUES (?,?,?,?,?,?)`, exposure.Alias, exposure.DatabaseID, exposure.ManagedDatabase, string(tables), exposure.Writable, exposure.TokenHash)
	return err
}

func (c *Catalog) UpdateAPIExposure(exposure *APIExposure) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	tables, err := json.Marshal(exposure.Tables)
	if err != nil {
		return err
	}
	result, err := c.db.Exec(`UPDATE api_exposures SET tables_json=?,writable=? WHERE alias=?`, string(tables), exposure.Writable, exposure.Alias)
	if err != nil {
		return err
	}
	return exposureAffected(result)
}

func (c *Catalog) RotateAPIExposureToken(alias, hash string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	result, err := c.db.Exec(`UPDATE api_exposures SET token_hash=? WHERE alias=?`, hash, alias)
	if err != nil {
		return err
	}
	return exposureAffected(result)
}

func (c *Catalog) DeleteAPIExposure(alias string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	result, err := c.db.Exec(`DELETE FROM api_exposures WHERE alias=?`, alias)
	if err != nil {
		return err
	}
	return exposureAffected(result)
}

func exposureAffected(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (c *Catalog) Path() string { return c.path }
