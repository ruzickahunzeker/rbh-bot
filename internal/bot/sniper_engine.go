package bot

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

type SniperTriggerKind string

const (
	SniperLaunch    SniperTriggerKind = "launch"
	SniperMigration SniperTriggerKind = "migration"
)

type SniperTask struct {
	TaskID         string
	ChainID        uint64
	WalletID       string
	Token          common.Address
	TriggerKind    SniperTriggerKind
	AmountInWei    *big.Int
	MaxSlippageBps uint64
	AntiMEV        bool
	HoneypotCheck  bool
	State          string
}

type OnSniperTriggerFn func(task SniperTask, simOutRaw *big.Int)

type SniperEngine struct {
	store       *Store
	shadow      *PaperShadowEngine
	quoter      ShadowQuoter
	onTriggered OnSniperTriggerFn
}

func NewSniperEngine(store *Store, shadow *PaperShadowEngine, quoter ShadowQuoter, onTriggered OnSniperTriggerFn) (*SniperEngine, error) {
	if store == nil {
		return nil, errors.New("bot store is required for sniper engine")
	}
	return &SniperEngine{
		store:       store,
		shadow:      shadow,
		quoter:      quoter,
		onTriggered: onTriggered,
	}, nil
}

func (e *SniperEngine) ArmTask(ctx context.Context, task SniperTask, now time.Time) (SniperTask, error) {
	if e == nil || e.store == nil || e.store.db == nil {
		return SniperTask{}, ErrBotStoreUnavailable
	}
	if task.TaskID == "" || task.ChainID == 0 || strings.TrimSpace(task.WalletID) == "" ||
		task.Token == (common.Address{}) || task.AmountInWei == nil || task.AmountInWei.Sign() <= 0 {
		return SniperTask{}, errors.New("invalid sniper task parameters")
	}
	if task.TriggerKind != SniperLaunch && task.TriggerKind != SniperMigration {
		return SniperTask{}, fmt.Errorf("unsupported sniper trigger_kind %q", task.TriggerKind)
	}
	if task.MaxSlippageBps == 0 {
		task.MaxSlippageBps = 1500
	}
	antiInt, hpInt := 0, 0
	if task.AntiMEV {
		antiInt = 1
	}
	if task.HoneypotCheck {
		hpInt = 1
	}
	task.State = "ARMED"
	nowStr := formatFixedUTC(now)
	if _, err := e.store.db.ExecContext(ctx,
		`INSERT INTO sniper_tasks(task_id, chain_id, wallet_id, token, trigger_kind, amount_in_wei, max_slippage_bps, anti_mev, honeypot_check, state, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,'ARMED',?,?)`,
		task.TaskID, task.ChainID, task.WalletID, strings.ToLower(task.Token.Hex()), string(task.TriggerKind),
		task.AmountInWei.String(), task.MaxSlippageBps, antiInt, hpInt, nowStr, nowStr,
	); err != nil {
		return SniperTask{}, err
	}
	return task, nil
}

func (e *SniperEngine) DisarmTask(ctx context.Context, chainID uint64, taskID string, now time.Time) error {
	if e == nil || e.store == nil || e.store.db == nil {
		return ErrBotStoreUnavailable
	}
	_, err := e.store.db.ExecContext(ctx,
		`UPDATE sniper_tasks SET state = 'CANCELLED', updated_at = ? WHERE task_id = ? AND chain_id = ? AND state = 'ARMED'`,
		formatFixedUTC(now), taskID, chainID,
	)
	return err
}

func (e *SniperEngine) ListArmed(ctx context.Context, chainID uint64) ([]SniperTask, error) {
	if e == nil || e.store == nil || e.store.db == nil {
		return nil, ErrBotStoreUnavailable
	}
	rows, err := e.store.db.QueryContext(ctx,
		`SELECT task_id, wallet_id, token, trigger_kind, amount_in_wei, max_slippage_bps, anti_mev, honeypot_check, state
		 FROM sniper_tasks WHERE chain_id = ? AND state = 'ARMED' ORDER BY created_at DESC`,
		chainID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SniperTask
	for rows.Next() {
		var t SniperTask
		var tokHex, kindStr, amtStr string
		var antiInt, hpInt int
		if err := rows.Scan(&t.TaskID, &t.WalletID, &tokHex, &kindStr, &amtStr, &t.MaxSlippageBps, &antiInt, &hpInt, &t.State); err != nil {
			return nil, err
		}
		t.ChainID = chainID
		t.Token = common.HexToAddress(tokHex)
		t.TriggerKind = SniperTriggerKind(kindStr)
		t.AmountInWei, _ = new(big.Int).SetString(amtStr, 10)
		t.AntiMEV = antiInt == 1
		t.HoneypotCheck = hpInt == 1
		out = append(out, t)
	}
	return out, rows.Err()
}

// MatchAndTrigger atomically fires all ARMED sniper tasks matching (chainID, token, triggerKind)
// exactly once, executes Shadow fills when Shadow mode is active, and invokes onTriggered.
func (e *SniperEngine) MatchAndTrigger(ctx context.Context, chainID uint64, token common.Address, kind SniperTriggerKind, now time.Time) ([]SniperTask, error) {
	if e == nil || e.store == nil || e.store.db == nil {
		return nil, ErrBotStoreUnavailable
	}
	tx, err := e.store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	tokHex := strings.ToLower(token.Hex())
	nowStr := formatFixedUTC(now)
	rows, err := tx.QueryContext(ctx,
		`SELECT task_id, wallet_id, amount_in_wei, max_slippage_bps, anti_mev, honeypot_check
		 FROM sniper_tasks WHERE chain_id = ? AND token = ? AND trigger_kind = ? AND state = 'ARMED'
		 ORDER BY created_at ASC`,
		chainID, tokHex, string(kind),
	)
	if err != nil {
		return nil, err
	}
	var matched []SniperTask
	for rows.Next() {
		var t SniperTask
		var amtStr string
		var antiInt, hpInt int
		if err := rows.Scan(&t.TaskID, &t.WalletID, &amtStr, &t.MaxSlippageBps, &antiInt, &hpInt); err != nil {
			rows.Close()
			return nil, err
		}
		t.ChainID = chainID
		t.Token = token
		t.TriggerKind = kind
		t.AmountInWei, _ = new(big.Int).SetString(amtStr, 10)
		t.AntiMEV = antiInt == 1
		t.HoneypotCheck = hpInt == 1
		t.State = "TRIGGERED"
		matched = append(matched, t)
	}
	rows.Close()

	for _, t := range matched {
		if _, err := tx.ExecContext(ctx,
			`UPDATE sniper_tasks SET state = 'TRIGGERED', updated_at = ? WHERE task_id = ? AND state = 'ARMED'`,
			nowStr, t.TaskID,
		); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	for i, t := range matched {
		simOut := big.NewInt(0)
		if e.shadow != nil && e.shadow.Enabled() && e.quoter != nil {
			outRaw, spotWei, qErr := e.quoter.QuoteShadowFill(ctx, t.ChainID, t.WalletID, t.Token, "buy", t.AmountInWei)
			if qErr == nil && outRaw != nil && outRaw.Sign() > 0 {
				simOut = outRaw
				if spotWei == nil || spotWei.Sign() <= 0 {
					spotWei = big.NewInt(1)
				}
				fillID := "snipe-fill-" + t.TaskID
				if e.shadow.RecordShadowFill(ctx, fillID, t.ChainID, t.WalletID, t.Token, "buy", t.AmountInWei, outRaw, spotWei, "sniper:"+t.TaskID, now) == nil {
					_, _ = e.store.db.ExecContext(ctx,
						`UPDATE sniper_tasks SET state = 'EXECUTED', updated_at = ? WHERE task_id = ?`,
						nowStr, t.TaskID,
					)
					matched[i].State = "EXECUTED"
				}
			}
		}
		if e.onTriggered != nil {
			e.onTriggered(matched[i], simOut)
		}
	}
	return matched, nil
}
