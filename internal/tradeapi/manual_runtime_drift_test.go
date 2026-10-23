package tradeapi

import (
	"context"
	"testing"
	"time"
)

// Sequence-owned trusted samples deliberately recover after a bad observation.
// Sticky rejection must prevent even asking for that recovered sample.
type runtimeSequenceClock struct {
	samples []manualRuntimeSample
	calls   int
}

func (s *runtimeSequenceClock) sample(ctx context.Context) (manualRuntimeSample, error) {
	i := s.calls
	s.calls++
	if i >= len(s.samples) {
		i = len(s.samples) - 1
	}
	return s.samples[i], ctx.Err()
}

func runtimeDriftSequence(wall, kind string) *runtimeSequenceClock {
	good := manualRuntimeSample{wall: wall, ticks: 10 * time.Nanosecond, provider: "trusted-fixture", session: "session-1"}
	bad, recovered := good, good
	switch kind {
	case "provider":
		bad.provider = "other-provider"
	case "session":
		bad.session = "other-session"
	case "ticks":
		bad.ticks--
	}
	recovered.ticks++
	return &runtimeSequenceClock{samples: []manualRuntimeSample{good, bad, recovered}}
}

func TestHTTPM4B22ClockDriftRecoveryIsSticky(t *testing.T) {
	for _, kind := range []string{"provider", "session", "ticks"} {
		t.Run(kind, func(t *testing.T) {
			source := runtimeDriftSequence("2026-10-16T00:00:00Z", kind)
			clock := manualRuntimeClock{source: source, maximumError: time.Second}
			if _, err := clock.read(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := clock.read(context.Background()); err == nil || !clock.invalid || clock.faultReason != "CLOCK_REVERSAL" {
				t.Fatal("drift accepted", err)
			}
			for i := 0; i < 2; i++ {
				if got, err := clock.read(context.Background()); err == nil || got != (manualRuntimeSample{}) {
					t.Fatal("recovery cleared sticky fault", got, err)
				}
			}
			if source.calls != 2 {
				t.Fatal("latched invocation queried recovered provider", source.calls)
			}
		})
	}
}

func TestHTTPM4B22ClockDriftDurableLatchAndRestart(t *testing.T) {
	for _, kind := range []string{"provider", "session", "ticks"} {
		for _, phase := range []string{"before_RPC", "final_writer"} {
			t.Run(kind+"/"+phase, func(t *testing.T) {
				d, s, path, v, id, request, clock, observer := runtimeVerifierFixture(t, "buy")
				before := foundationEconomicSnapshot(t, s)
				source := runtimeDriftSequence(clock.wall.UTC().Format(time.RFC3339Nano), kind)
				faultCall, expectedRPC := 2, 0
				if phase == "final_writer" {
					faultCall, expectedRPC = 6, 1
					good, bad, recovered := source.samples[0], source.samples[1], source.samples[2]
					source.samples = []manualRuntimeSample{good, good, good, good, good, bad, recovered}
				}
				v.timeSource = source
				auditBefore, alertBefore := count(t, s.db, "canary_runtime_audit"), count(t, s.db, "canary_alert_outbox")
				candidate, err := v.capture(context.Background(), id, request)
				if err == nil || candidate != (manualRuntimeCandidate{}) || source.calls != faultCall || observer.calls != expectedRPC || count(t, s.db, "manual_submission_gate_evidence") != 0 || before != foundationEconomicSnapshot(t, s) {
					t.Fatal("drift produced candidate/observation/economic effect", candidate, err)
				}
				var fault, generation int
				var reason string
				if err := s.db.QueryRow(`SELECT fault_latched,generation,fault_reason FROM manual_gate_clock_state`).Scan(&fault, &generation, &reason); err != nil || fault != 1 || generation != 2 || reason != "CLOCK_REVERSAL" {
					t.Fatal("fault not durable", fault, generation, reason, err)
				}
				if count(t, s.db, "manual_gate_clock_events") != 2 || count(t, s.db, "canary_runtime_audit") != auditBefore+1 || count(t, s.db, "canary_alert_outbox") != alertBefore+1 {
					t.Fatal("fault audit/alert not exactly once")
				}
				_, s = bindingReopen(t, d, path)
				v.store = s
				v.timeSource = &runtimeSequenceClock{samples: []manualRuntimeSample{source.samples[len(source.samples)-1]}}
				candidate, err = v.capture(context.Background(), id, request)
				if err == nil || candidate != (manualRuntimeCandidate{}) || observer.calls != expectedRPC || count(t, s.db, "manual_submission_gate_evidence") != 0 || before != foundationEconomicSnapshot(t, s) || count(t, s.db, "manual_gate_clock_events") != 2 || count(t, s.db, "canary_runtime_audit") != auditBefore+1 || count(t, s.db, "canary_alert_outbox") != alertBefore+1 {
					t.Fatal("restart or healthy samples cleared latch", candidate, err)
				}
			})
		}
	}
}
