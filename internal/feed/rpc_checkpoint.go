package feed

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

type RPCCheckpoint struct {
	ChainID, Number uint64
	Hash            common.Hash
}
type rpcBlockCommit struct {
	RPCCheckpoint
	ParentHash common.Hash
	Audits     []ReceiptAudit
}

func (s *Store) RPCCheckpoint(ctx context.Context, chainID uint64) (RPCCheckpoint, bool, error) {
	p := RPCCheckpoint{ChainID: chainID}
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT block_number,block_hash FROM feed_rpc_checkpoints WHERE chain_id=?`, chainID).Scan(&p.Number, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return p, false, nil
	}
	if err != nil {
		return p, false, err
	}
	p.Hash = common.HexToHash(hash)
	if len(hash) != 66 || p.Hash == (common.Hash{}) {
		return p, false, fmt.Errorf("invalid RPC checkpoint hash")
	}
	return p, true, nil
}

func (s *Store) initializeRPC(ctx context.Context, p RPCCheckpoint) error {
	if p.ChainID == 0 || p.Hash == (common.Hash{}) {
		return fmt.Errorf("invalid RPC baseline")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO feed_rpc_checkpoints(chain_id,block_number,block_hash,updated_at) VALUES(?,?,?,?) ON CONFLICT(chain_id) DO NOTHING`, p.ChainID, p.Number, p.Hash.Hex(), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO feed_rpc_source_audit(chain_id,kind,to_block,detail,created_at) VALUES(?,'INITIAL_BASELINE',?,'Historical blocks through baseline excluded; sequencer sequence retained independently',?)`, p.ChainID, p.Number, time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) haltRPC(ctx context.Context, chainID, from, to uint64, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `UPDATE feed_progress SET degraded=1,degraded_reason=?,updated_at=? WHERE id=1`, reason, stamp)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO feed_rpc_source_audit(chain_id,kind,from_block,to_block,detail,created_at) VALUES(?,'HALTED',?,?,?,?)`, chainID, from, to, reason, stamp)
	if err != nil {
		return err
	}
	return tx.Commit()
}
