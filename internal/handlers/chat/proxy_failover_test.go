package chat

import (
	"testing"

	"9router/proxy/internal/db"
)

// NoteProxyResult must never panic on missing pieces — it sits on the hot path.
func TestNoteProxyResult_NilSafe(t *testing.T) {
	h := &ChatHandler{}
	h.NoteProxyResult(nil, "http://a:8080", false)
	h.NoteProxyResult(&ConnectionData{}, "", false)
	h.NoteProxyResult(&ConnectionData{ProxyPoolID: ""}, "http://a:8080", false)
	h.NoteProxyResult(&ConnectionData{ProxyPoolID: "x"}, "http://a:8080", true)
}

// resolveProxyURLs with no pool binding resolves direct (nil).
func TestResolveProxyURLs_NoBinding(t *testing.T) {
	h := &ChatHandler{}
	urls, strict := h.resolveProxyURLs(nil)
	if urls != nil || strict {
		t.Fatalf("nil connData must resolve direct, got %v %v", urls, strict)
	}
	urls, _ = h.resolveProxyURLs(&ConnectionData{})
	if urls != nil {
		t.Fatalf("empty pool id must resolve direct, got %v", urls)
	}
}

// End-to-end through a real DB: bound pool fully cooling down falls back to
// the best-live (lowest latency, passed) pool's URLs.
func TestResolveProxyURLs_FallbackToBestLive(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	repo := db.NewRepo(database)
	dead, err := repo.InsertProxyPool(db.ProxyPoolData{
		Name: "dead-pool", ProxyURL: "http://dead:8080", Type: "http",
	})
	if err != nil {
		t.Fatalf("insert dead pool: %v", err)
	}
	live, err := repo.InsertProxyPool(db.ProxyPoolData{
		Name: "live-pool", ProxyURL: "http://live:8080", Type: "http",
	})
	if err != nil {
		t.Fatalf("insert live pool: %v", err)
	}
	deadID := dead["id"].(string)
	liveID := live["id"].(string)
	if err := repo.SetProxyPoolStatus(deadID, "passed", 50); err != nil {
		t.Fatalf("stamp dead: %v", err)
	}
	if err := repo.SetProxyPoolStatus(liveID, "passed", 10); err != nil {
		t.Fatalf("stamp live: %v", err)
	}

	h := NewChatHandler(repo)
	// Park every URL of the bound pool.
	deadPool, err := repo.GetProxyPool(deadID)
	if err != nil {
		t.Fatalf("get dead pool: %v", err)
	}
	for _, u := range deadPool.URLs {
		for i := 0; i < 3; i++ {
			deadPool.ReportURLResult(u, false)
		}
	}

	urls, _ := h.resolveProxyURLs(&ConnectionData{ProxyPoolID: deadID})
	if len(urls) != 1 || urls[0] != "http://live:8080" {
		t.Fatalf("expected fallback to live pool URL, got %v", urls)
	}

	// No passed pool anywhere: resolves direct.
	if err := repo.SetProxyPoolStatus(liveID, "failed", 0); err != nil {
		t.Fatalf("fail live: %v", err)
	}
	urls, _ = h.resolveProxyURLs(&ConnectionData{ProxyPoolID: deadID})
	if urls != nil {
		t.Fatalf("no live pool must resolve direct, got %v", urls)
	}
}

// Strict on either side fails closed: never direct, strict flag propagates.
func TestResolveProxyURLs_StrictPropagates(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	repo := db.NewRepo(database)
	strict, err := repo.InsertProxyPool(db.ProxyPoolData{
		Name: "strict-pool", ProxyURL: "http://s:8080", Type: "http", StrictProxy: true,
	})
	if err != nil {
		t.Fatalf("insert strict pool: %v", err)
	}
	strictID := strict["id"].(string)
	if err := repo.SetProxyPoolStatus(strictID, "passed", 5); err != nil {
		t.Fatalf("stamp strict: %v", err)
	}

	h := NewChatHandler(repo)
	urls, isStrict := h.resolveProxyURLs(&ConnectionData{ProxyPoolID: strictID})
	if len(urls) != 1 || !isStrict {
		t.Fatalf("expected live strict URL + strict flag, got %v %v", urls, isStrict)
	}
}
