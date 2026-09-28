package app

import (
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

func TestParseFlagsAutoStart(t *testing.T) {
	cfg := config.Default()
	if cfg.AutoStart {
		t.Fatal("expected default AutoStart to be false")
	}

	options, err := parseFlags([]string{"--auto-start"}, &cfg)
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}

	if !cfg.AutoStart {
		t.Fatal("expected AutoStart to be true after --auto-start flag")
	}
	if options.overrides.autoStart == nil || !*options.overrides.autoStart {
		t.Fatal("expected overrides.autoStart to be set to true")
	}
}

func TestSameDataConfigAutoStart(t *testing.T) {
	active := config.Default()
	active.AutoStart = false

	dto := runtimeConfigDTO(active)
	if !sameDataConfig(dto, active, dataConfigOverrides{}) {
		t.Fatal("expected sameDataConfig to return true for matching AutoStart")
	}

	dto.AutoStart = true
	if sameDataConfig(dto, active, dataConfigOverrides{}) {
		t.Fatal("expected sameDataConfig to return false when AutoStart differs")
	}
}

func TestRuntimeConfigDTOAutoStart(t *testing.T) {
	cfg := config.Default()
	cfg.AutoStart = true

	dto := runtimeConfigDTO(cfg)
	if !dto.AutoStart {
		t.Fatal("expected runtimeConfigDTO to copy AutoStart=true")
	}
}

func TestDataConfigOverridesApplyAutoStart(t *testing.T) {
	cfg := config.Default()
	cfg.AutoStart = false

	autoStart := true
	overrides := dataConfigOverrides{autoStart: &autoStart}
	overrides.Apply(&cfg)

	if !cfg.AutoStart {
		t.Fatal("expected Apply to set AutoStart to true")
	}
}
