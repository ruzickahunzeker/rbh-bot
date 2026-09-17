package feed

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	"github.com/ethereum/go-ethereum/common"
)

func TestObservationIdentityIgnoresDeliverySequence(t *testing.T) {
	base := Observation{
		ChainID:          ChainID,
		Source:           SourceSequencer,
		SourceSequence:   10,
		TransactionHash:  common.HexToHash("0x1234"),
		StableActionPath: "intent/000/buy",
		PayloadJSON:      json.RawMessage(`{"type":"transaction_intent"}`),
		ObservedAt:       time.Unix(1_700_000_400, 0).UTC(),
	}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	replayed := base
	replayed.SourceSequence = 99
	replayed.ObservedAt = base.ObservedAt.Add(time.Hour)
	if base.ID() != replayed.ID() {
		t.Fatalf("delivery sequence changed observation identity: %s != %s", base.ID(), replayed.ID())
	}
	otherSource := base
	otherSource.Source = SourceRPC
	if base.ID() == otherSource.ID() {
		t.Fatal("source provenance must remain part of observation identity")
	}
}

func TestPonsCurveIntentNormalizerUsesConfirmedRegistryContext(t *testing.T) {
	root := filepath.Join("..", "..", "rbh-validation-package", "fixtures", "historical", "candidates")
	value := parser.New()
	launch := loadReceipt(t, filepath.Join(root, "0x45142a829ca4ce83909c0f87408b27f70da8a1853a101921fffc20ff5fc08b10.json"))
	if events, err := value.ParseReceipt(launch); err != nil || len(events) == 0 {
		t.Fatalf("bootstrap confirmed Pons launch: events=%d err=%v", len(events), err)
	}

	path := filepath.Join(root, "0x04fc83e1a45cbbc7a0dc6e18dd996f91225c7d97158412560dc0d2fbec5c9d2d.json")
	tx, sender := loadCapturedTransaction(t, path)
	normalizer := NewPonsCurveIntentNormalizer(value)
	if !normalizer.Potential(tx) {
		t.Fatal("registered Pons Curve buy was not recognized as potential intent")
	}
	observations, err := normalizer.Normalize(tx, sender, SourceSequencer, 77, time.Unix(1_700_000_500, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 1 {
		t.Fatalf("got %d observations, want 1", len(observations))
	}
	var payload normalizedIntentPayload
	if err := json.Unmarshal(observations[0].PayloadJSON, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Protocol != parser.ProtocolPonsV2.String() || payload.IntentKind != "buy" || payload.TransactionHash != tx.Hash().Hex() {
		t.Fatalf("unexpected normalized payload: %#v", payload)
	}
	if observations[0].StableActionPath != "intent/000/buy" || observations[0].SourceSequence != 77 {
		t.Fatalf("unexpected observation metadata: %#v", observations[0])
	}
}
