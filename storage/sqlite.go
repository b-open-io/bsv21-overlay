package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/4chain-ag/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
)

type SQLiteStorage struct {
	DB *sql.DB
}

func NewSQLiteStorage(conn string) (*SQLiteStorage, error) {
	if db, err := sql.Open("sqlite3", conn); err != nil {
		return nil, err
	} else if _, err = db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		return nil, err
	} else if _, err = db.Exec("PRAGMA synchronous=NORMAL;"); err != nil {
		return nil, err
	} else if _, err = db.Exec("PRAGMA busy_timeout=5000;"); err != nil {
		return nil, err
	} else if _, err = db.Exec("PRAGMA temp_store=MEMORY;"); err != nil {
		return nil, err
	} else if _, err = db.Exec("PRAGMA mmap_size=30000000000;"); err != nil {
		return nil, err
	} else if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS transactions(
		txid TEXT PRIMARY KEY,
		beef BLOB NOT NULL,
		created_at TEXT NOT NULL DEFAULT current_timestamp,
		updated_at TEXT NOT NULL DEFAULT current_timestamp
	)`); err != nil {
		return nil, err
	} else if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS outputs(
		outpoint TEXT NOT NULL,
		topic TEXT NOT NULL,
		height INTEGER,
		idx BIGINT NOT NULL DEFAULT 0,
		satoshis BIGINT NOT NULL,
		script BLOB NOT NULL,
		consumes TEXT NOT NULL DEFAULT '[]',
		consumed_by TEXT NOT NULL DEFAULT '[]',
		spent BOOL NOT NULL DEFAULT false,
		created_at TEXT NOT NULL DEFAULT current_timestamp,
		updated_at TEXT NOT NULL DEFAULT current_timestamp,
		PRIMARY KEY(outpoint, topic)
	)`); err != nil {
		return nil, err
	} else if _, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_outputs_topic ON outputs(topic)`); err != nil {
		return nil, err
	} else if _, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_outputs_topic_height_idx ON outputs(topic, height, idx)`); err != nil {
		return nil, err
	} else if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS applied_transactions(
		txid TEXT NOT NULL,
		topic TEXT NOT NULL,
		created_at TEXT NOT NULL DEFAULT current_timestamp,
		updated_at TEXT NOT NULL DEFAULT current_timestamp,
		PRIMARY KEY(txid, topic)
	);`); err != nil {
		return nil, err
	} else {
		db.SetMaxOpenConns(1)
		return &SQLiteStorage{DB: db}, nil
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
	if _, err = s.DB.ExecContext(ctx, `
        INSERT INTO outputs(topic, outpoint, height, idx, satoshis, script, spent, consumes)
        VALUES(?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(outpoint, topic) DO NOTHING`,
		utxo.Topic,
		utxo.Outpoint.String(),
		utxo.BlockHeight,
		utxo.BlockIdx,
		utxo.Satoshis,
		utxo.Script,
		utxo.Spent,
		consumed,
	); err != nil {
		return err
	} else if len(utxo.Beef) > 0 {
		if _, err = s.DB.ExecContext(ctx, `
			INSERT INTO transactions(txid, beef)
			VALUES(?1, ?2)
			ON CONFLICT(txid) DO NOTHING`,
			utxo.Outpoint.Txid.String(),
			utxo.Beef,
		); err != nil {
			return err
		}
	}
	return
}

func (s *SQLiteStorage) FindOutput(ctx context.Context, outpoint *overlay.Outpoint, topic *string, spent *bool, includeBEEF bool) (output *engine.Output, err error) {
	output = &engine.Output{
		Outpoint: *outpoint,
	}
	query := `SELECT topic, height, idx, satoshis, script, spent, consumes, consumed_by
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
	if err := s.DB.QueryRowContext(ctx, query, args...).Scan(
		&output.Topic,
		&output.BlockHeight,
		&output.BlockIdx,
		&output.Satoshis,
		&output.Script,
		&output.Spent,
		&consumes,
		&consumedBy,
	); err == sql.ErrNoRows {
		return nil, engine.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(consumes, &output.OutputsConsumed); err != nil {
		return nil, err
	} else if err := json.Unmarshal(consumedBy, &output.ConsumedBy); err != nil {
		return nil, err
	} else if includeBEEF {
		if err := s.DB.QueryRowContext(ctx, `
			SELECT beef
			FROM transactions
			WHERE txid = ?`,
			outpoint.Txid.String(),
		).Scan(&output.Beef); err != nil {
			return nil, err
		}
	}
	return
}

func (s *SQLiteStorage) FindOutputs(ctx context.Context, outpoints []*overlay.Outpoint, topic *string, spent *bool, includeBEEF bool) ([]*engine.Output, error) {
	var outputs []*engine.Output
	if len(outpoints) == 0 {
		return nil, nil
	}

	var query strings.Builder
	query.WriteString(`SELECT topic, outpoint, height, idx, satoshis, script, spent, consumes, consumed_by
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
	rows, err := s.DB.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		output := &engine.Output{}
		var consumes []byte
		var consumedBy []byte
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
		if includeBEEF {
			if err := s.DB.QueryRowContext(ctx, `
				SELECT beef
				FROM transactions
				WHERE txid = ?`,
				output.Outpoint.Txid.String(),
			).Scan(&output.Beef); err != nil {
				return nil, err
			}
		}
		outputs = append(outputs, output)

	}
	return outputs, nil
}

func (s *SQLiteStorage) FindOutputsForTransaction(ctx context.Context, txid *chainhash.Hash, includeBEEF bool) ([]*engine.Output, error) {
	var outputs []*engine.Output
	query := `SELECT topic, outpoint, height, idx, satoshis, script, spent, consumes, consumed_by
        FROM outputs
        WHERE outpoint LIKE ?
        ORDER BY outpoint ASC`
	rows, err := s.DB.QueryContext(ctx, query, txid.String()+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		output := &engine.Output{}
		var consumes []byte
		var consumedBy []byte
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
		); err != nil {
			return nil, err
		} else if outpoint, err := overlay.NewOutpointFromString(op); err != nil {
			return nil, err
		} else if err := json.Unmarshal(consumes, &output.OutputsConsumed); err != nil {
			return nil, err
		} else if err := json.Unmarshal(consumedBy, &output.ConsumedBy); err != nil {
			return nil, err
		} else if includeBEEF {
			if err := s.DB.QueryRowContext(ctx, `
				SELECT beef
				FROM transactions
				WHERE txid = ?`,
				txid.String(),
			).Scan(&output.Beef); err != nil {
				return nil, err
			}
		} else {
			output.Outpoint = *outpoint
		}
		outputs = append(outputs, output)
	}
	return outputs, nil
}

func (s *SQLiteStorage) FindUTXOsForTopic(ctx context.Context, topic string, since float64, includeBEEF bool) ([]*engine.Output, error) {
	var outputs []*engine.Output
	query := `
        SELECT outpoint, height, idx, satoshis, script, spent, consumes, consumed_by
        FROM outputs
        WHERE topic = ? AND height >= ?
        ORDER BY height ASC, idx ASC`
	rows, err := s.DB.QueryContext(ctx, query, topic, since)
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
			&output.Outpoint.OutputIndex,
			&output.BlockHeight,
			&output.BlockIdx,
			&output.Satoshis,
			&output.Script,
			&output.Spent,
			&consumes,
			&consumedBy,
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
		if includeBEEF {
			if err := s.DB.QueryRowContext(ctx, `
				SELECT beef
				FROM transactions
				WHERE txid = ?`,
				output.Outpoint.Txid.String(),
			).Scan(&output.Beef); err != nil {
				return nil, err
			}
		}
		outputs = append(outputs, output)
	}
	return outputs, nil
}

func (s *SQLiteStorage) DeleteOutput(ctx context.Context, outpoint *overlay.Outpoint, topic string) error {
	_, err := s.DB.ExecContext(ctx, `
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
	_, err := s.DB.ExecContext(ctx, query, args...)
	return err
}

func (s *SQLiteStorage) MarkUTXOAsSpent(ctx context.Context, outpoint *overlay.Outpoint, topic string) error {
	_, err := s.DB.ExecContext(ctx, `
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
	_, err := s.DB.ExecContext(ctx, query, args...)
	return err
}

func (s *SQLiteStorage) UpdateConsumedBy(ctx context.Context, outpoint *overlay.Outpoint, topic string, consumedBy []*overlay.Outpoint) error {
	if consumedByStr, err := json.Marshal(consumedBy); err != nil {
		return err
	} else {
		_, err := s.DB.ExecContext(ctx, `
			UPDATE outputs
			SET consumed_by = ?
			WHERE topic = ? AND outpoint = ?`,
			consumedByStr,
			topic,
			outpoint.Txid.String(),
		)
		return err
	}
}

func (s *SQLiteStorage) UpdateTransactionBEEF(ctx context.Context, txid *chainhash.Hash, beef []byte) error {
	_, err := s.DB.ExecContext(ctx, `
        UPDATE transactions
        SET beef = ?
        WHERE txid = ?`,
		beef,
		txid.String(),
	)
	return err
}

func (s *SQLiteStorage) UpdateOutputBlockHeight(ctx context.Context, outpoint *overlay.Outpoint, topic string, blockHeight uint32, blockIndex uint64) error {
	_, err := s.DB.ExecContext(ctx, `
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
	_, err := s.DB.ExecContext(ctx, `
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
	err := s.DB.QueryRowContext(ctx, `
        SELECT EXISTS(SELECT 1 FROM applied_transactions WHERE topic = ? AND txid = ?)`,
		tx.Topic,
		tx.Txid.String(),
	).Scan(&exists)
	return exists, err
}

func (s *SQLiteStorage) Close() error {
	return s.DB.Close()
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return "?" + strings.Repeat(",?", n-1)
}

func toInterfaceSlice(strs []string) []interface{} {
	ifaces := make([]interface{}, len(strs))
	for i, s := range strs {
		ifaces[i] = s
	}
	return ifaces
}
