package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Harvi8/AegisCore/agent-server/model"
	"github.com/Harvi8/AegisCore/agent-server/testfixture"
)

// The Dimension Hole rows are derived from the catalog loaded from SRO_SHARD. These loaders take that catalog as an
// argument so import-reference cannot parse them before the catalog is loaded (which imported 0 regions before).

func writeClientText(t *testing.T, name, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestLoadGameWorldReferenceDataAppliesLifecycleOnlyToCatalogWorlds(t *testing.T) {
	path := writeClientText(t, "gameworlddata.txt",
		"10\tINS_QNO_RM_OLDWOMAN_6\t1\t100\t8\txxx\n63\tINS_GOD_TOGUI_35_50_LEVEL_1\t1\t50\t4\tGROUP_GOD_TOGUI_35_50_LEVEL_1\n")

	rows, err := loadGameWorldReferenceData(path, testfixture.DimensionHoles())
	if err != nil {
		t.Fatalf("loadGameWorldReferenceData() error = %v", err)
	}

	if rows[0].WorldDurationMS != 0 || rows[0].WorldEmptyRemainMS != 0 {
		t.Fatalf("non-Dimension Hole world got lifecycle values: %+v", rows[0])
	}
	if rows[1].WorldDurationMS != uint64(model.DimensionHoleWorldDurationMS) || rows[1].WorldEmptyRemainMS != uint64(model.DimensionHoleWorldEmptyRemainMS) {
		t.Fatalf("Dimension Hole world 63 lifecycle = %+v", rows[1])
	}
}

func TestLoadDimensionHoleWorldRegionReferenceDataRejectsEmptyCatalog(t *testing.T) {
	path := writeClientText(t, "refregion.txt", "")

	if _, err := loadDimensionHoleWorldRegionReferenceData(path, nil); err == nil {
		t.Fatal("loadDimensionHoleWorldRegionReferenceData() with no catalog = nil error, want an error")
	}
}

func TestDimensionHoleStockStartPositionsFollowGivenCatalog(t *testing.T) {
	if got := dimensionHoleStockStartPositions(nil); len(got) != 0 {
		t.Fatalf("start positions with no catalog = %d rows, want 0", len(got))
	}
	if got, want := len(dimensionHoleStockStartPositions(testfixture.DimensionHoles())), len(testfixture.DimensionHoles()); got != want {
		t.Fatalf("start positions = %d rows, want %d", got, want)
	}
}
