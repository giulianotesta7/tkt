package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// migrate0043 finishes 0043_catalog_folders.sql inside its transaction (issue
// #484). The script copies departments and desks into folders but cannot fold
// case beyond ASCII, so this step computes every folder's name_key with
// domain.FolderNameKey, renames the later of two colliding siblings by
// appending " (<department or desk id>)" until its key is free, creates the
// sibling-unique index over the keys and drops the script's mapping table.
//
// Folders are visited in id order, which is department order and then desk
// order, so the earliest row of a group keeps its name. The loop has no pass
// limit: each rename only lengthens one name, and a name is accepted the
// moment its key is unused among its siblings, so any legal existing catalog
// upgrades.
func migrate0043(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT f.id, f.parent_id, f.name, m.source_id
		FROM folders f JOIN folder_backfill_map m ON m.folder_id = f.id ORDER BY f.id`)
	if err != nil {
		return fmt.Errorf("read backfilled folders: %w", err)
	}
	type backfilled struct {
		id, parent, source int64
		name               string
	}
	var folders []backfilled
	for rows.Next() {
		var f backfilled
		var parent sql.NullInt64
		if err := rows.Scan(&f.id, &parent, &f.name, &f.source); err != nil {
			rows.Close()
			return err
		}
		f.parent = parent.Int64
		folders = append(folders, f)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Every original key is reserved first, so a rename never takes a name an
	// existing sibling still holds unsuffixed further down the id order.
	taken := map[int64]map[string]int64{}
	for _, f := range folders {
		if taken[f.parent] == nil {
			taken[f.parent] = map[string]int64{}
		}
		key := domain.FolderNameKey(f.name)
		if _, ok := taken[f.parent][key]; !ok {
			taken[f.parent][key] = f.id
		}
	}
	for _, f := range folders {
		siblings := taken[f.parent]
		name, key := f.name, domain.FolderNameKey(f.name)
		for owner, ok := siblings[key]; ok && owner != f.id; owner, ok = siblings[key] {
			name = fmt.Sprintf("%s (%d)", name, f.source)
			key = domain.FolderNameKey(name)
		}
		siblings[key] = f.id
		if _, err := tx.ExecContext(ctx, `UPDATE folders SET name = ?, name_key = ? WHERE id = ?`, name, key, f.id); err != nil {
			return fmt.Errorf("name folder %d: %w", f.id, err)
		}
	}

	if _, err := tx.ExecContext(ctx, `DROP TABLE folder_backfill_map;
		CREATE UNIQUE INDEX idx_folders_sibling_name ON folders(ifnull(parent_id, 0), name_key)`); err != nil {
		return fmt.Errorf("finish folders: %w", err)
	}
	return nil
}
