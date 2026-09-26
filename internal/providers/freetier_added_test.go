package providers

import "testing"

func TestNewFreeTierProviders_Wired(t *testing.T) {
	for _, id := range []string{"requesty", "sea-lion", "neosantara", "aionlabs", "iflytek", "kira", "yolo-auto"} {
		cfg, ok := KnownProviders[id]
		if !ok {
			t.Fatalf("KnownProviders missing %q", id)
		}
		if cfg.BaseURL == "" || cfg.AuthHeader == "" || cfg.AuthScheme == "" {
			t.Errorf("%q incomplete: %+v", id, cfg)
		}
		if got := ResolveAlias(id); got != id {
			t.Errorf("ResolveAlias(%q) = %q, want identity", id, got)
		}
		if len(GetProviderModels(id)) == 0 {
			t.Errorf("no models registered for %q", id)
		}
	}
	if ResolveAlias("ry") != "requesty" {
		t.Error("alias ry must resolve to requesty")
	}
	if ResolveAlias("sl") != "sea-lion" {
		t.Error("alias sl must resolve to sea-lion")
	}
	if ResolveAlias("nst") != "neosantara" {
		t.Error("alias nst must resolve to neosantara")
	}
	if ResolveAlias("alo") != "aionlabs" {
		t.Error("alias alo must resolve to aionlabs")
	}
	if ResolveAlias("xf") != "iflytek" {
		t.Error("alias xf must resolve to iflytek")
	}
	if ResolveAlias("ki") != "kira" {
		t.Error("alias ki must resolve to kira")
	}
	if ResolveAlias("ya") != "yolo-auto" {
		t.Error("alias ya must resolve to yolo-auto")
	}
	// agnes: free-tier OpenAI-compatible, passthrough models (no seed), original registry had passthroughModels: true
	agnCfg, ok := KnownProviders["agnes"]
	if !ok {
		t.Fatal("KnownProviders missing agnes")
	}
	if agnCfg.BaseURL == "" || agnCfg.AuthHeader == "" || agnCfg.AuthScheme == "" {
		t.Errorf("agnes incomplete: %+v", agnCfg)
	}
	if ResolveAlias("agnes") != "agnes" {
		t.Errorf("ResolveAlias(agnes) = %q, want identity", ResolveAlias("agnes"))
	}
	if ResolveAlias("agn") != "agnes" {
		t.Error("alias agn must resolve to agnes")
	}
	if ResolveAlias("agnes-ai") != "agnes" {
		t.Error("alias agnes-ai must resolve to agnes")
	}
	if ResolveAlias("kira") == "kiro" || ResolveAlias("kr") != "kiro" {
		t.Error("kira must not collide with kiro")
	}
}
