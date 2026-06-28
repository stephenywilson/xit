package opencodehook

import (
	"strings"
	"sync"
	"testing"
)

func TestOpenCodeTurnAccumulatesSameUserMessage(t *testing.T) {
	home := t.TempDir()
	turnKey := MakeTurnKey("s1", "m1")
	for i := 1; i <= 3; i++ {
		st, err := IncrementTurnState(home, turnKey, 100*i)
		if err != nil {
			t.Fatalf("increment failed: %v", err)
		}
		if st.RunCount != i {
			t.Fatalf("run_count=%d want %d", st.RunCount, i)
		}
	}
	st := ReadTurnStateByKey(home, turnKey)
	if st == nil || st.RunCount != 3 || st.SavedTokensTotal != 600 {
		t.Fatalf("wrong state: %+v", st)
	}
}

func TestOpenCodeNewUserMessageResetsCount(t *testing.T) {
	home := t.TempDir()
	_, _ = IncrementTurnState(home, MakeTurnKey("s1", "m1"), 100)
	_, _ = IncrementTurnState(home, MakeTurnKey("s1", "m1"), 100)
	st, err := IncrementTurnState(home, MakeTurnKey("s1", "m2"), 300)
	if err != nil {
		t.Fatalf("increment failed: %v", err)
	}
	if st.RunCount != 1 || st.SavedTokensTotal != 300 {
		t.Fatalf("new user message must start at 1/current saved tokens, got %+v", st)
	}
}

func TestOpenCodeSessionIsolation(t *testing.T) {
	home := t.TempDir()
	_, _ = IncrementTurnState(home, MakeTurnKey("s1", "m1"), 100)
	st, _ := IncrementTurnState(home, MakeTurnKey("s2", "m1"), 200)
	if st.RunCount != 1 || st.SavedTokensTotal != 200 {
		t.Fatalf("session s2 polluted by s1, got %+v", st)
	}
	if other := ReadTurnState(home, "s1", "m1"); other == nil || other.SavedTokensTotal != 100 {
		t.Fatalf("s1 state missing/changed: %+v", other)
	}
}

func TestOpenCodeConcurrentTurnUpdate(t *testing.T) {
	home := t.TempDir()
	turnKey := MakeTurnKey("s1", "m1")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = IncrementTurnState(home, turnKey, 7)
		}()
	}
	wg.Wait()
	st := ReadTurnStateByKey(home, turnKey)
	if st == nil || st.RunCount != 20 || st.SavedTokensTotal != 140 {
		t.Fatalf("expected atomic count=20 saved=140, got %+v", st)
	}
}

func TestOpenCodeMissingTurnSignalNoState(t *testing.T) {
	home := t.TempDir()
	st, err := IncrementTurnState(home, "", 100)
	if err != nil {
		t.Fatalf("missing turn signal should fail open: %v", err)
	}
	if st != nil || len(TurnStateFiles(home)) != 0 {
		t.Fatalf("missing user message id must not create fake turn state: st=%+v files=%v", st, TurnStateFiles(home))
	}
}

func TestOpenCodeMakeTurnKeyOpaqueStableAndIsolated(t *testing.T) {
	k1 := MakeTurnKey("ses_abc", "msg_abc")
	k2 := MakeTurnKey("ses_abc", "msg_abc")
	k3 := MakeTurnKey("ses_abc", "msg_other")
	k4 := MakeTurnKey("ses_other", "msg_abc")
	if k1 == "" || len(k1) != 24 {
		t.Fatalf("turn key should be 24 hex chars, got %q", k1)
	}
	if k1 != k2 {
		t.Fatalf("same session/message must produce same key: %q vs %q", k1, k2)
	}
	if k1 == k3 || k1 == k4 {
		t.Fatalf("different session/message must produce different keys: %q %q %q", k1, k3, k4)
	}
	if strings.Contains(k1, "ses_abc") || strings.Contains(k1, "msg_abc") {
		t.Fatalf("turn key must not expose raw IDs: %q", k1)
	}
}
