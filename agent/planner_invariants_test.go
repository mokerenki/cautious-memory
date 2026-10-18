package agent

import (
	"strings"
	"testing"
)

func TestAppendInvariantHeuristics(t *testing.T) {
	out := AppendInvariantHeuristics("planner base")
	if !strings.Contains(out, "业务逻辑不变量启发式") {
		t.Fatal("missing heuristics section")
	}
	if !strings.Contains(out, "BOLA") {
		t.Fatal("missing BOLA guidance")
	}
	if !strings.HasPrefix(out, "planner base") {
		t.Fatal("base lost")
	}
}

func TestFormatTaskInvariants(t *testing.T) {
	if FormatTaskInvariants("") != "" {
		t.Fatal("empty should stay empty")
	}
	out := FormatTaskInvariants("对象只能被 owner 读取\n金额服务端重算")
	if !strings.Contains(out, "任务不变量") || !strings.Contains(out, "owner") {
		t.Fatal(out)
	}
}
