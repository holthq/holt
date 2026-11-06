package version

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestVersions：编进来的 mygo 是 go.mod 里锁的那个；mygo.json 的版本号在 CHANGELOG 里有一节。
func TestVersions(t *testing.T) {
	mod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if v := Mygo(); !strings.Contains(string(mod), MygoModule+" "+v) || v != MygoPinned {
		t.Errorf("Mygo() = %q，go.mod 里不是这个", v)
	}
	if !strings.HasPrefix(Go(), "go") || !strings.Contains(Platform(), "/") {
		t.Errorf("Go() = %q, Platform() = %q", Go(), Platform())
	}
	if s := String(); !strings.HasPrefix(s, "Holt dev (mygo v") {
		t.Errorf("String() = %q", s)
	}

	var cfg struct{ Version string }
	b, err := os.ReadFile("../../mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version == "" || !strings.Contains(string(log), "\n## v"+cfg.Version+" ") {
		t.Errorf("mygo.json 的版本 %q 在 CHANGELOG.md 里没有一节（## v%s …）", cfg.Version, cfg.Version)
	}
}

// TestAppName：应用叫 Holt——mygo.json 的名字和 bundle id（单实例锁、open -a、Dock 跟着它）、版本那一行的开头。
func TestAppName(t *testing.T) {
	var cfg struct{ Name, Identifier string }
	b, err := os.ReadFile("../../mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "Holt" || cfg.Identifier != "io.github.holthq.holt" || !strings.HasPrefix(String(), "Holt ") {
		t.Errorf("mygo.json %+v、String() %q，应是 Holt / io.github.holthq.holt", cfg, String())
	}
}
