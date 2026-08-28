package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRecoveryStateMachinePersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	if err := s.RecordGap(ctx, "common", 41, "journal coverage incomplete"); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginRecovery(ctx, "common"); err != nil {
		t.Fatal(err)
	}
	record, found, err := s.Recovery(ctx, "common")
	if err != nil || !found || record.State != RecoveryResynchronizing || record.Position != 41 || record.Cause != "journal coverage incomplete" {
		t.Fatalf("recovery = %+v found=%v err=%v", record, found, err)
	}
	if err := s.MarkRecoveryReady(ctx, "common", 50); err != nil {
		t.Fatal(err)
	}
	ready, records, err := s.RecoveryReady(ctx)
	if err != nil || !ready || len(records) != 1 || records[0].State != RecoveryReady {
		t.Fatalf("ready=%v records=%+v err=%v", ready, records, err)
	}
}

func TestPermanentRecoveryRequiresExplicitAcknowledgement(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	if err := s.RecordGap(ctx, "channel:9", 12, "channel inaccessible"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkRecoveryPermanent(ctx, "channel:9", "channel inaccessible"); err != nil {
		t.Fatal(err)
	}
	ready, records, err := s.RecoveryReady(ctx)
	if err != nil || ready || len(records) != 1 || records[0].State != RecoveryPermanentDegraded || records[0].Acknowledged {
		t.Fatalf("ready=%v records=%+v err=%v", ready, records, err)
	}
	if err := s.AcknowledgeRecovery(ctx, "channel:9"); err != nil {
		t.Fatal(err)
	}
	ready, records, err = s.RecoveryReady(ctx)
	if err != nil || !ready || !records[0].Acknowledged {
		t.Fatalf("ready=%v records=%+v err=%v", ready, records, err)
	}
}

func TestRecoveryReadinessWaitsForEveryRequiredBox(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	for _, box := range []string{"common", "channel:1"} {
		if err := s.RecordGap(ctx, box, 1, "gap"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MarkRecoveryReady(ctx, "common", 2); err != nil {
		t.Fatal(err)
	}
	if ready, _, err := s.RecoveryReady(ctx); err != nil || ready {
		t.Fatalf("ready=%v err=%v", ready, err)
	}
	if err := s.MarkRecoveryReady(ctx, "channel:1", 3); err != nil {
		t.Fatal(err)
	}
	if ready, _, err := s.RecoveryReady(ctx); err != nil || !ready {
		t.Fatalf("ready=%v err=%v", ready, err)
	}
}

func TestRecoverySourcesAreBoundedAndQuarantineCompletes(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	for _, payload := range [][]byte{[]byte("one"), []byte("two")} {
		if _, err := s.AppendJournal(ctx, payload, testTime()); err != nil {
			t.Fatal(err)
		}
	}
	journal, err := s.PendingJournal(ctx, 1)
	if err != nil || len(journal) != 1 || string(journal[0].Payload) != "one" {
		t.Fatalf("journal=%+v err=%v", journal, err)
	}
	jid, _ := s.AppendJournal(ctx, []byte("capture"), testTime())
	if _, err := s.Capture(ctx, Capture{JournalID: jid, Quarantine: &QuarantineInput{SourceKey: "q1", Kind: "old", Payload: []byte("raw")}}); err != nil {
		t.Fatal(err)
	}
	quarantine, err := s.PendingQuarantine(ctx, 1)
	if err != nil || len(quarantine) != 1 || quarantine[0].SourceKey != "q1" {
		t.Fatalf("quarantine=%+v err=%v", quarantine, err)
	}
	if err := s.CompleteQuarantine(ctx, quarantine[0].ID); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.PendingQuarantine(ctx, 1); err != nil || len(rows) != 0 {
		t.Fatalf("remaining=%+v err=%v", rows, err)
	}
}

func testTime() time.Time { return time.Unix(123, 0).UTC() }

func TestRecordGapPreservesAcknowledgedPermanentForSameCause(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	if err := s.RecordGap(ctx, "channel:9", 12, "channel inaccessible"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkRecoveryPermanent(ctx, "channel:9", "channel inaccessible"); err != nil {
		t.Fatal(err)
	}
	if err := s.AcknowledgeRecovery(ctx, "channel:9"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordGap(ctx, "channel:9", 12, "channel inaccessible"); err != nil {
		t.Fatal(err)
	}
	record, _, err := s.Recovery(ctx, "channel:9")
	if err != nil || record.State != RecoveryPermanentDegraded || !record.Acknowledged {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	if err := s.RecordGap(ctx, "channel:9", 13, "new recoverable evidence"); err != nil {
		t.Fatal(err)
	}
	record, _, err = s.Recovery(ctx, "channel:9")
	if err != nil || record.State != RecoveryDegraded || record.Acknowledged {
		t.Fatalf("record=%+v err=%v", record, err)
	}
}

func TestPreviousSchemaMigrationCopiesDegradedConsistency(t *testing.T) {
	ctx := context.Background()
	dir := privateStoreDirectory(t)
	path := filepath.Join(dir, "telegram.db")
	s, err := Open(ctx, Config{Path: path, AccountKey: "deployment", AuthorizedUserID: 42, GotdVersion: "v0.161.0", TGLayer: 228})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO consistency(account_key,box,position,degraded,reason) VALUES(?,?,?,?,?)", s.accountKey, "common", 41, 1, "old gap"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM recovery; UPDATE schema_meta SET version=3"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	reopened, err := Open(ctx, Config{Path: path, AccountKey: "deployment", AuthorizedUserID: 42, GotdVersion: "v0.161.0", TGLayer: 228})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	record, found, err := reopened.Recovery(ctx, "common")
	if err != nil || !found || record.Position != 41 || record.Cause != "old gap" || record.State != RecoveryDegraded {
		t.Fatalf("record=%+v found=%v err=%v", record, found, err)
	}
}
