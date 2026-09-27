package market

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCSV(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "data.csv")
	content := "# comment line\ntime,open,high,low,close,volume,suspended,limit_up\n" +
		"2024-01-01,10,11,9,10,1000,false,false\n" +
		"2024-01-02,10.5,10.8,10.4,10.6,1200,true,false\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	bars, err := LoadCSV(p, "AAA")
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 2 {
		t.Fatalf("bars = %d", len(bars))
	}
	if bars[0].Symbol != "AAA" || bars[0].Close != 10 {
		t.Errorf("bar0 = %+v", bars[0])
	}
	if !bars[1].Suspended || bars[1].Volume != 1200 {
		t.Errorf("bar1 flags = %+v", bars[1])
	}
}

func TestLoadCSVMissingColumn(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.csv")
	if err := os.WriteFile(p, []byte("time,open,high,low\n2024-01-01,1,1,1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCSV(p, "AAA"); err == nil {
		t.Error("expected error for missing close column")
	}
}
