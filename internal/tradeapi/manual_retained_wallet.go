package tradeapi

import (
	"context"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

// Until a separately reviewed multi-generation wallet/reorg accounting model
// exists, a retained MANUAL nonce claim is a lifetime wallet reuse boundary.
// It is NOT a reason to hide history or stop recovery of that same artifact.
// Schema 045 repeats this check in the admission command's writer transaction.
func checkManualWalletReusable(ctx context.Context, q queryRow, id Identity) error {
	var retained bool
	address := strings.ToLower(common.HexToAddress(id.WalletAddress).Hex())
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manual_retained_wallet_claims
 WHERE wallet_id=? OR lower(wallet_address)=?)`, id.WalletID, address).Scan(&retained); err != nil {
		return err
	}
	if retained {
		return rejectManualInput("MANUAL_WALLET_REUSE_UNSUPPORTED")
	}
	return nil
}
