package ql

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/docker/containerd/snapshot"
	"github.com/docker/containerd/snapshot/storage"
	"github.com/pkg/errors"

	_ "github.com/cznic/ql/driver"
)

type qlMetastore struct {
	dbfile string
}

// NewMetaStore returns a snapshot MetaStore for storage of metadata related to
// a snapshot driver backed by a ql file database. This implementation is
// strongly consistent and does all metadata changes in a transaction to prevent
// against process crashes causing inconsistent metadata state.
//
// To view mtastore with a utility use https://godoc.org/github.com/cznic/ql/ql
func NewMetaStore(ctx context.Context, dbfile string) (storage.MetaStore, error) {
	ms := &qlMetastore{
		dbfile: dbfile,
	}
	schema := []string{
		`CREATE TABLE IF NOT EXISTS Snapshots (Name string NOT NULL, Parent int, Active bool, Readonly bool);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS SnapshotsName on Snapshots (Name);`,
		`CREATE INDEX IF NOT EXISTS SnapshotsParent on Snapshots (Parent);`,
	}

	err := ms.doTransaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for _, q := range schema {
			_, err := tx.ExecContext(ctx, q)
			if err != nil {
				return errors.Wrap(err, "failed to exec create")
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return ms, nil
}

func (ms *qlMetastore) doTransaction(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
	return ms.doDatabase(ctx, func(ctx context.Context, db *sql.DB) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return errors.Wrap(err, "failed to start transaction")
		}

		if err = fn(ctx, tx); err != nil {
			if err1 := tx.Rollback(); err1 != nil {
				err = errors.Wrapf(err, "rollback failure: %v", err1)
			}
			return err
		}
		if err := tx.Commit(); err != nil {
			return errors.Wrap(err, "failed to commit")
		}
		return nil
	})
}

func (ms *qlMetastore) doDatabase(ctx context.Context, fn func(context.Context, *sql.DB) error) error {
	dbFile := filepath.Join(ms.dbfile)
	// TODO: acquire lock on file

	db, err := sql.Open("ql", dbFile)
	if err != nil {
		return errors.Wrap(err, "failed to open database file")
	}
	defer db.Close()

	return fn(ctx, db)
}

func (ms *qlMetastore) Stat(ctx context.Context, key string) (snapshot.Info, error) {
	var (
		info   snapshot.Info
		parent sql.NullString
		active bool
	)
	info.Name = key

	err := ms.doTransaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, "SELECT b.Name, a.Active, a.Readonly FROM Snapshots as a LEFT JOIN (SELECT id() as ID, Name FROM Snapshots) as b ON a.Parent==b.ID WHERE a.Name=$1", key).Scan(&parent, &active, &info.Readonly)
		if err != nil {
			if err == sql.ErrNoRows {
				// TODO: return snapshot package error
				return errors.Errorf("snapshot %v not found", key)
			}
			return errors.Wrap(err, "select stat failed")
		}
		return nil
	})
	if err != nil {
		return snapshot.Info{}, err
	}
	if parent.Valid {
		info.Parent = parent.String
	}
	if active {
		info.Kind = snapshot.KindActive
	} else {
		info.Kind = snapshot.KindCommitted
	}

	return info, nil
}

func (ms *qlMetastore) Walk(ctx context.Context, fn func(context.Context, snapshot.Info) error) error {
	return ms.doDatabase(ctx, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx, "SELECT a.Name, b.Name as Parent, a.Active, a.Readonly FROM Snapshots as a LEFT JOIN (SELECT id() as ID, Name FROM Snapshots) as b ON a.Parent==b.ID")
		if err != nil {
			return err
		}

		var (
			info   snapshot.Info
			active bool
			parent sql.NullString
		)
		for rows.Next() {
			if err := rows.Scan(&info.Name, &parent, &active, &info.Readonly); err != nil {
				return err
			}

			info.Parent = parent.String
			if active {
				info.Kind = snapshot.KindActive
			} else {
				info.Kind = snapshot.KindCommitted

			}

			if err := fn(ctx, info); err != nil {
				return err
			}
		}

		return nil
	})
}

func (ms *qlMetastore) CreateActive(ctx context.Context, key string, opts storage.CreateActiveOpts) (a storage.Active, err error) {
	err = ms.doTransaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var parentID *int64
		if opts.Parent != "" {
			var (
				id           int64
				parentActive bool
			)

			err := tx.QueryRowContext(ctx, "SELECT id(), Active FROM Snapshots WHERE Name=$1", opts.Parent).Scan(&id, &parentActive)
			if err != nil {
				// TODO: return specific snapshot does not exist error if row not found
				return errors.Wrap(err, "failed to lookup parent")
			}
			if parentActive {
				// TODO: make snapshot package error
				return errors.Errorf("cannot create active from active")
			}

			parentID = &id
		}

		res, err := tx.ExecContext(ctx, "INSERT INTO Snapshots (Name, Parent, Active, Readonly) VALUES ($1, $2, true, $3)", key, parentID, opts.Readonly)
		if err != nil {
			// TODO: handle already exists
			return errors.Wrap(err, "failed to insert active record")
		}
		if n, err := res.RowsAffected(); err != nil {
			return errors.Wrap(err, "failed to get number of affected rows")
		} else if n == 0 {
			return errors.Errorf("failed to insert active row")
		}

		insertID, err := res.LastInsertId()
		if err != nil {
			return errors.Wrap(err, "failed to get insert id")
		}
		a.ID = fmt.Sprintf("%d", insertID)

		if opts.Create != nil {
			if err := opts.Create(a.ID); err != nil {
				return errors.Wrap(err, "create callback failed")
			}
		}

		a.ParentIDs, err = ms.parents(ctx, tx, parentID)
		if err != nil {
			return errors.Wrap(err, "failed to get parent chain")
		}

		return nil
	})
	if err != nil {
		return storage.Active{}, err
	}

	a.Readonly = opts.Readonly
	return
}

func (ms *qlMetastore) GetActive(ctx context.Context, key string) (a storage.Active, err error) {
	err = ms.doTransaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var id int
		var parentVal sql.NullInt64
		err := tx.QueryRowContext(ctx, "SELECT id(), Parent, Readonly FROM Snapshots WHERE Name=$1 AND Active=true", key).Scan(&id, &parentVal, &a.Readonly)
		if err != nil {
			// TODO: check for no rows
			return errors.Wrapf(err, "failed to get snapshot: %v", key)
		}

		a.ID = fmt.Sprintf("%d", id)

		var parentID *int64
		if parentVal.Valid {
			parentID = &parentVal.Int64
		}

		a.ParentIDs, err = ms.parents(ctx, tx, parentID)
		if err != nil {
			return errors.Wrap(err, "failed to get parent chain")
		}
		return nil
	})
	if err != nil {
		return storage.Active{}, err
	}

	return
}

func (ms *qlMetastore) parents(ctx context.Context, tx *sql.Tx, parent *int64) ([]string, error) {
	parents := []string{}
	if parent == nil {
		return parents, nil
	}

	parentStmt, err := tx.PrepareContext(ctx, "SELECT Parent FROM Snapshots WHERE id()=$1")
	if err != nil {
		return nil, err
	}

	for {
		parents = append(parents, fmt.Sprintf("%d", *parent))

		var parentValue sql.NullInt64
		if err := parentStmt.QueryRowContext(ctx, *parent).Scan(&parentValue); err != nil {
			return nil, errors.Wrap(err, "failed to lookup parent")
		}
		if parentValue.Valid {
			parent = &parentValue.Int64
		} else {
			break
		}
	}

	return parents, nil
}

func (ms *qlMetastore) Remove(ctx context.Context, key string, cleanup func(id string) error) error {
	err := ms.doTransaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var (
			id     int64
			active bool
		)
		err := tx.QueryRowContext(ctx, "SELECT id(), Active FROM Snapshots WHERE Name=$1", key).Scan(&id, &active)
		if err != nil {
			// TODO: return snapshot package defined error
			return errors.Wrap(err, "failed to lookup by key")
		}
		if !active {
			var children int
			err = tx.QueryRowContext(ctx, "SELECT count() FROM Snapshots WHERE Parent=$1", id).Scan(&children)
			if err != nil {
				return errors.Wrap(err, "failed to lookup children")
			}
			if children != 0 {
				// TODO: wrap package defined error
				return errors.Errorf("has children: %d", children)
			}
		}

		if err := cleanup(fmt.Sprintf("%d", id)); err != nil {
			return errors.Wrap(err, "failed to cleanup")
		}

		_, err = tx.ExecContext(ctx, "DELETE FROM Snapshots WHERE id()=$1", id)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

func (ms *qlMetastore) Commit(ctx context.Context, name, key string) error {
	return ms.doTransaction(ctx, func(ctx context.Context, tx *sql.Tx) error {

		var id int
		err := tx.QueryRowContext(ctx, "SELECT id() FROM Snapshots WHERE Name=$1 AND Active=true", key).Scan(&id)
		if err != nil {
			// TODO: check for no rows
			return errors.Wrapf(err, "failed to get snapshot: %v", key)
		}

		res, err := tx.ExecContext(ctx, "UPDATE Snapshots SET Name=$1, Active=false, Readonly=true WHERE id()=$2", name, id)
		if err != nil {
			return err
		}

		n, err := res.RowsAffected()
		if err != nil {
			return err
		}

		if n == 0 {
			// TODO: use package defined error
			return errors.Errorf("name already exists")
		}

		// TODO: callback to allow making updates to ID during transaction

		return nil
	})
}
