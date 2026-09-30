package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("BINANCE_BASE_URL", "")
	t.Setenv("BINANCE_ORDER_BOOK_DEPTH", "")
	t.Setenv("BINANCE_DRY_RUN", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg != Default() {
		t.Fatalf("got %+v, want defaults", cfg)
	}
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("BINANCE_BASE_URL", "http://localhost:8080/api/v3")
	t.Setenv("BINANCE_ORDER_BOOK_DEPTH", "100")
	t.Setenv("BINANCE_DRY_RUN", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	b := cfg.Binance
	if b.BaseURL != "http://localhost:8080/api/v3" || b.OrderBookDepth != 100 || !b.DryRun {
		t.Fatalf("env overrides not applied: %+v", b)
	}
}

func TestLoadInvalid(t *testing.T) {
	for name, env := range map[string][2]string{
		"depth is not a number": {"BINANCE_ORDER_BOOK_DEPTH", "many"},
		"depth is zero":         {"BINANCE_ORDER_BOOK_DEPTH", "0"},
		"dry run is not a bool": {"BINANCE_DRY_RUN", "maybe"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(env[0], env[1])
			if _, err := Load(); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
