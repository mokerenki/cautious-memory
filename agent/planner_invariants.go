package agent

// InvariantHeuristicsSection is always appended to the planner system prompt.
// It activates even when the task has no operator-supplied invariants.
const InvariantHeuristicsSection = `

━━ 业务逻辑不变量启发式（常驻）━━
当探索图 / 资产上出现以下事实时，优先派发对应意图（可与其它侦察并行，但应早于深度利用）：
1. **对象带 owner / tenant / user_id / org_id 字段** → 派「跨租户/跨用户读取与修改」意图（BOLA/IDOR）。
2. **观察到数值型 price / amount / total / quantity / discount** → 派「客户端价格/数量篡改」意图。
3. **多步工作流（request→approve、create→confirm、申请→发放）** → 派「跳过前置步骤 / 重放终态请求」意图。
4. **一次性 token / coupon / invite / 配额** → 派「并发消费、预签发、配额边界」意图。
5. 若任务上下文提供了 **invariants** 列表，将其视为已确认不变量，优先围绕它们生成违反尝试；业务逻辑猎手 skill（business-logic-hunter）应在深度利用前尽早调度。
`

// AppendInvariantHeuristics returns base + the standing heuristics section.
func AppendInvariantHeuristics(base string) string {
	return base + InvariantHeuristicsSection
}

// FormatTaskInvariants turns a newline-separated invariants field into a prompt block.
func FormatTaskInvariants(raw string) string {
	if raw == "" {
		return ""
	}
	return "\n\n【任务不变量 invariants】\n" + raw + "\n（违反任一条即可记为 finding，并填写 invariant_id / violation_demo / blast_radius）\n"
}
