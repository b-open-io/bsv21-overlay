package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/4chain-ag/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	_ "github.com/mattn/go-sqlite3"
)

type SQLiteStorage struct {
	wDB *sql.DB
	rDB *sql.DB
}

func NewSQLiteStorage(conn string) (*SQLiteStorage, error) {
	if wdb, err := sql.Open("sqlite3", conn); err != nil {
		return nil, err
	} else if _, err = wdb.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		return nil, err
	} else if _, err = wdb.Exec("PRAGMA synchronous=NORMAL;"); err != nil {
		return nil, err
	} else if _, err = wdb.Exec("PRAGMA busy_timeout=5000;"); err != nil {
		return nil, err
	} else if _, err = wdb.Exec("PRAGMA temp_store=MEMORY;"); err != nil {
		return nil, err
	} else if _, err = wdb.Exec("PRAGMA mmap_size=30000000000;"); err != nil {
		return nil, err
		// } else if _, err = wdb.Exec(`CREATE TABLE IF NOT EXISTS transactions(
		// 	txid TEXT PRIMARY KEY,
		// 	beef BLOB NOT NULL,
		// 	topic TEXT,
		// 	created_at TEXT NOT NULL DEFAULT current_timestamp,
		// 	updated_at TEXT NOT NULL DEFAULT current_timestamp
		// )`); err != nil {
		// 	return nil, err
	} else if _, err = wdb.Exec(`CREATE TABLE IF NOT EXISTS outputs(
		outpoint TEXT NOT NULL,
		topic TEXT NOT NULL,
		height INTEGER,
		idx BIGINT NOT NULL DEFAULT 0,
		satoshis BIGINT NOT NULL,
		script BLOB NOT NULL,
		beef BLOB NOT NULL,
		consumes TEXT NOT NULL DEFAULT '[]',
		consumed_by TEXT NOT NULL DEFAULT '[]',
		dependencies TEXT NOT NULL DEFAULT '[]',
		spent BOOL NOT NULL DEFAULT false,
		created_at TEXT NOT NULL DEFAULT current_timestamp,
		updated_at TEXT NOT NULL DEFAULT current_timestamp,
		PRIMARY KEY(outpoint, topic)
	)`); err != nil {
		return nil, err
	} else if _, err = wdb.Exec(`CREATE INDEX IF NOT EXISTS idx_outputs_topic ON outputs(topic)`); err != nil {
		return nil, err
	} else if _, err = wdb.Exec(`CREATE INDEX IF NOT EXISTS idx_outputs_topic_height_idx ON outputs(topic, height, idx)`); err != nil {
		return nil, err
	} else if _, err = wdb.Exec(`CREATE TABLE IF NOT EXISTS applied_transactions(
		txid TEXT NOT NULL,
		topic TEXT NOT NULL,
		created_at TEXT NOT NULL DEFAULT current_timestamp,
		updated_at TEXT NOT NULL DEFAULT current_timestamp,
		PRIMARY KEY(txid, topic)
	);`); err != nil {
		return nil, err
	} else if rdb, err := sql.Open("sqlite3", conn); err != nil {
		return nil, err
	} else if _, err = rdb.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		return nil, err
	} else if _, err = rdb.Exec("PRAGMA synchronous=NORMAL;"); err != nil {
		return nil, err
	} else if _, err = rdb.Exec("PRAGMA busy_timeout=5000;"); err != nil {
		return nil, err
	} else if _, err = rdb.Exec("PRAGMA temp_store=MEMORY;"); err != nil {
		return nil, err
	} else if _, err = rdb.Exec("PRAGMA mmap_size=30000000000;"); err != nil {
		return nil, err
	} else {
		wdb.SetMaxOpenConns(1)
		return &SQLiteStorage{wDB: wdb, rDB: rdb}, nil
	}

	// CREATE INDEX idx_outputs_txid_vout ON outputs(txid, vout);
	// CREATE INDEX idx_outputs_topic_height_idx ON outputs(topic, height, idx);

}

func (s *SQLiteStorage) InsertOutput(ctx context.Context, utxo *engine.Output) (err error) {
	consumed := []byte("[]")
	if len(utxo.OutputsConsumed) > 0 {
		if consumed, err = json.Marshal(utxo.OutputsConsumed); err != nil {
			return
		}
	}
	dependencies := []byte("[]")
	if len(utxo.Dependencies) > 0 {
		if dependencies, err = json.Marshal(utxo.Dependencies); err != nil {
			return
		}
	}
	_, err = s.wDB.ExecContext(ctx, `
        INSERT INTO outputs(topic, outpoint, height, idx, satoshis, script, spent, consumes, dependencies, beef)
        VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(outpoint, topic) DO NOTHING`,
		utxo.Topic,
		utxo.Outpoint.String(),
		utxo.BlockHeight,
		utxo.BlockIdx,
		utxo.Satoshis,
		utxo.Script,
		utxo.Spent,
		consumed,
		dependencies,
		utxo.Beef,
	)
	return
}

func (s *SQLiteStorage) FindOutput(ctx context.Context, outpoint *overlay.Outpoint, topic *string, spent *bool, includeBEEF bool) (output *engine.Output, err error) {
	output = &engine.Output{
		Outpoint: *outpoint,
	}
	query := `SELECT topic, height, idx, satoshis, script, spent, consumes, consumed_by, dependencies, beef
        FROM outputs
        WHERE outpoint = ? `
	args := []interface{}{outpoint.String()}
	if topic != nil {
		query += "AND topic = ? "
		args = append(args, *topic)
	}
	if spent != nil {
		query += "AND spent = ? "
		args = append(args, *spent)
	}
	var consumes []byte
	var consumedBy []byte
	var dependencies []byte
	if err := s.rDB.QueryRowContext(ctx, query, args...).Scan(
		&output.Topic,
		&output.BlockHeight,
		&output.BlockIdx,
		&output.Satoshis,
		&output.Script,
		&output.Spent,
		&consumes,
		&consumedBy,
		&dependencies,
		&output.Beef,
	); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(consumes, &output.OutputsConsumed); err != nil {
		return nil, err
	} else if err := json.Unmarshal(consumedBy, &output.ConsumedBy); err != nil {
		return nil, err
	} else if err := json.Unmarshal(dependencies, &output.Dependencies); err != nil {
		return nil, err
	}
	return
}

func (s *SQLiteStorage) FindOutputs(ctx context.Context, outpoints []*overlay.Outpoint, topic *string, spent *bool, includeBEEF bool) ([]*engine.Output, error) {
	var outputs []*engine.Output
	if len(outpoints) == 0 {
		return nil, nil
	}

	var query strings.Builder
	query.WriteString(`SELECT topic, outpoint, height, idx, satoshis, script, spent, consumes, consumed_by, dependencies, beef
        FROM outputs
        WHERE outpoint IN (` + placeholders(len(outpoints)) + ") ")
	args := make([]interface{}, 0, len(outpoints)+2)
	for _, outpoint := range outpoints {
		args = append(args, outpoint.String())
	}
	if topic != nil {
		query.WriteString("AND topic = ? ")
		args = append(args, *topic)
	}
	if spent != nil {
		query.WriteString("AND spent = ? ")
		args = append(args, *spent)
	}
	rows, err := s.rDB.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		output := &engine.Output{}
		var consumes []byte
		var consumedBy []byte
		var dependencies []byte
		var op string
		if err := rows.Scan(
			&output.Topic,
			&op,
			&output.BlockHeight,
			&output.BlockIdx,
			&output.Satoshis,
			&output.Script,
			&output.Spent,
			&consumes,
			&consumedBy,
			&dependencies,
			&output.Beef,
		); err != nil {
			return nil, err
		} else if outpoint, err := overlay.NewOutpointFromString(op); err != nil {
			return nil, err
		} else if err := json.Unmarshal(consumes, &output.OutputsConsumed); err != nil {
			return nil, err
		} else if err := json.Unmarshal(consumedBy, &output.ConsumedBy); err != nil {
			return nil, err
		} else if err := json.Unmarshal(dependencies, &output.Dependencies); err != nil {
			return nil, err
		} else {
			output.Outpoint = *outpoint
		}
		outputs = append(outputs, output)

	}
	return outputs, nil
}

func (s *SQLiteStorage) FindOutputsForTransaction(ctx context.Context, txid *chainhash.Hash, includeBEEF bool) ([]*engine.Output, error) {
	var outputs []*engine.Output
	query := `SELECT topic, outpoint, height, idx, satoshis, script, spent, consumes, consumed_by, dependencies, beef
        FROM outputs
        WHERE outpoint LIKE ?
        ORDER BY outpoint ASC`
	rows, err := s.rDB.QueryContext(ctx, query, txid.String()+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		output := &engine.Output{}
		var consumes []byte
		var consumedBy []byte
		var dependencies []byte
		var op string
		if err := rows.Scan(
			&output.Topic,
			&op,
			&output.BlockHeight,
			&output.BlockIdx,
			&output.Satoshis,
			&output.Script,
			&output.Spent,
			&consumes,
			&consumedBy,
			&dependencies,
			&output.Beef,
		); err != nil {
			return nil, err
		} else if outpoint, err := overlay.NewOutpointFromString(op); err != nil {
			return nil, err
		} else if err := json.Unmarshal(consumes, &output.OutputsConsumed); err != nil {
			return nil, err
		} else if err := json.Unmarshal(consumedBy, &output.ConsumedBy); err != nil {
			return nil, err
		} else if err := json.Unmarshal(dependencies, &output.Dependencies); err != nil {
			return nil, err
		} else {
			output.Outpoint = *outpoint
		}
		outputs = append(outputs, output)
	}
	return outputs, nil
}

func (s *SQLiteStorage) FindUTXOsForTopic(ctx context.Context, topic string, since uint32, includeBEEF bool) ([]*engine.Output, error) {
	var outputs []*engine.Output
	query := `
        SELECT outpoint, height, idx, satoshis, script, spent, consumes, consumed_by, beef
        FROM outputs
        WHERE topic = ? AND height >= ?
        ORDER BY height ASC, idx ASC`
	rows, err := s.rDB.QueryContext(ctx, query, topic, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		output := &engine.Output{
			Topic: topic,
		}
		var op string
		var consumes []byte
		var consumedBy []byte
		if err := rows.Scan(
			&op,
			&output.BlockHeight,
			&output.BlockIdx,
			&output.Satoshis,
			&output.Script,
			&output.Spent,
			&consumes,
			&consumedBy,
			&output.Beef,
		); err != nil {
			return nil, err
		} else if outpoint, err := overlay.NewOutpointFromString(op); err != nil {
			return nil, err
		} else if err := json.Unmarshal(consumes, &output.OutputsConsumed); err != nil {
			return nil, err
		} else if err := json.Unmarshal(consumedBy, &output.ConsumedBy); err != nil {
			return nil, err
		} else {
			output.Outpoint = *outpoint
		}
		outputs = append(outputs, output)
	}
	return outputs, nil
}

func (s *SQLiteStorage) DeleteOutput(ctx context.Context, outpoint *overlay.Outpoint, topic string) error {
	_, err := s.wDB.ExecContext(ctx, `
        DELETE FROM outputs
        WHERE topic = ? AND outpoint = ?`,
		topic,
		outpoint.String(),
	)
	return err
}

func (s *SQLiteStorage) DeleteOutputs(ctx context.Context, outpoints []*overlay.Outpoint, topic string) error {
	query := `
        DELETE FROM outputs
        WHERE topic = ? AND outpoint IN (` + placeholders(len(outpoints)) + ")"
	args := make([]interface{}, 0, len(outpoints)+1)
	args = append(args, topic)
	for _, outpoint := range outpoints {
		args = append(args, outpoint.String())
	}
	_, err := s.wDB.ExecContext(ctx, query, args...)
	return err
}

func (s *SQLiteStorage) MarkUTXOAsSpent(ctx context.Context, outpoint *overlay.Outpoint, topic string) error {
	_, err := s.wDB.ExecContext(ctx, `
        UPDATE outputs
        SET spent = true
        WHERE topic = ? AND outpoint = ?`,
		topic,
		outpoint.String(),
	)
	return err
}

func (s *SQLiteStorage) MarkUTXOsAsSpent(ctx context.Context, outpoints []*overlay.Outpoint, topic string) error {
	query := `
        UPDATE outputs
        SET spent = true
        WHERE topic = ? AND outpoint IN (` + placeholders(len(outpoints)) + ")"
	args := make([]interface{}, 0, len(outpoints)+1)
	args = append(args, topic)
	for _, outpoint := range outpoints {
		args = append(args, outpoint.String())
	}
	_, err := s.wDB.ExecContext(ctx, query, args...)
	return err
}

func (s *SQLiteStorage) UpdateConsumedBy(ctx context.Context, outpoint *overlay.Outpoint, topic string, consumedBy []*overlay.Outpoint) error {
	if consumedByStr, err := json.Marshal(consumedBy); err != nil {
		return err
	} else {
		_, err := s.wDB.ExecContext(ctx, `
			UPDATE outputs
			SET consumed_by = ?
			WHERE topic = ? AND outpoint = ?`,
			consumedByStr,
			topic,
			outpoint.String(),
		)
		return err
	}
}

func (s *SQLiteStorage) UpdateTransactionBEEF(ctx context.Context, txid *chainhash.Hash, beef []byte) error {
	_, err := s.wDB.ExecContext(ctx, `
        UPDATE transactions
        SET beef = ?
        WHERE txid = ?`,
		beef,
		txid.String(),
	)
	return err
}

func (s *SQLiteStorage) UpdateOutputBEEF(ctx context.Context, outpoint *overlay.Outpoint, topic string, beef []byte, blockHeight uint32, blockIndex uint64) error {
	_, err := s.wDB.ExecContext(ctx, `
		UPDATE outputs
		SET beef = ?, height = ?, idx = ?
		WHERE topic = ? AND outpoint = ?`,
		beef,
		blockHeight,
		blockIndex,
		topic,
		outpoint.String(),
	)
	return err
}

func (s *SQLiteStorage) UpdateOutputBlockHeight(ctx context.Context, outpoint *overlay.Outpoint, topic string, blockHeight uint32, blockIndex uint64) error {
	_, err := s.wDB.ExecContext(ctx, `
        UPDATE outputs
        SET height = ?, idx = ?
        WHERE topic = ? AND outpoint = ?`,
		blockHeight,
		blockIndex,
		topic,
		outpoint.String(),
	)
	return err
}

func (s *SQLiteStorage) InsertAppliedTransaction(ctx context.Context, tx *overlay.AppliedTransaction) error {
	_, err := s.wDB.ExecContext(ctx, `
        INSERT INTO applied_transactions(topic, txid)
        VALUES(?, ?)
        ON CONFLICT(topic, txid) DO NOTHING`,
		tx.Topic,
		tx.Txid.String(),
	)
	return err
}

func (s *SQLiteStorage) DoesAppliedTransactionExist(ctx context.Context, tx *overlay.AppliedTransaction) (bool, error) {
	var exists bool
	err := s.rDB.QueryRowContext(ctx, `
        SELECT EXISTS(SELECT 1 FROM applied_transactions WHERE topic = ? AND txid = ?)`,
		tx.Topic,
		tx.Txid.String(),
	).Scan(&exists)
	return exists, err
}

func (s *SQLiteStorage) Close() error {
	s.rDB.Close()
	return s.wDB.Close()
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return "?" + strings.Repeat(",?", n-1)
}
