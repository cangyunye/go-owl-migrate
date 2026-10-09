package serve

// AI 事实层：把用户已有的连接事实（数据源档案、当前激活配置）接入 /ai/plan。
//
// 设计不变量（与哨兵协议同一套）：
//   - LLM 只看到档案的名称/类型/schema 摘要，永不接触真实密码；
//   - 计划产物里档案端点携带"哨兵 DSN"（真实 DSN 换哨兵密码），会话存储
//     只存档案名称——明文 DSN 只在 confirm 时由服务端从档案解密回填；
//   - 档案端点因此不出现在 credential_slots 里：UI 不向用户重复要密码。

import (
	"fmt"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/ai"
	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/configbuild"
	"gopkg.in/yaml.v3"
)

// planFactsBlock renders the connection-facts context appended to the route
// and slot-extraction messages: saved datasource profiles (name/type/schema,
// no secrets) plus a masked summary of the currently active config. With this
// block the LLM can pick a profile instead of demanding the user retype a
// DSN it already stored.
func (s *Server) planFactsBlock() string {
	var b strings.Builder
	if store, err := s.dsStore(); err == nil {
		if infos, lerr := store.List(); lerr == nil && len(infos) > 0 {
			b.WriteString("\n\n【可用数据源档案】用户已保存并测试过的连接。匹配时端点填 profile=档案名，服务端负责真实连接与密码：\n")
			for _, in := range infos {
				line := "- " + in.Name + "（" + in.Type
				if in.Schema != "" {
					line += ", schema " + in.Schema
				}
				line += "）"
				if in.Remark != "" {
					line += "：" + in.Remark
				}
				b.WriteString(line + "\n")
			}
		}
	}
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	if cfg != nil && (cfg.Source.Type != "" || cfg.Target.Type != "") {
		b.WriteString("\n【当前激活配置】")
		if cfg.Source.Type != "" {
			b.WriteString("source: " + cfg.Source.Type + " " + config.MaskDSN(cfg.Source.DSN) + " schema=" + cfg.Source.Schema)
		}
		if cfg.Target.Type != "" {
			b.WriteString(" → target: " + cfg.Target.Type + " " + config.MaskDSN(cfg.Target.DSN) + " schema=" + cfg.Target.Schema)
		}
		b.WriteString("（用户提到\"当前配置/现有配置\"时优先按此理解）\n")
	}
	return b.String()
}

// resolveProfilesInSlots substitutes stored datasource profiles for endpoints
// that reference one by name: type/schema/DSN come from the vault, with the
// DSN's password swapped for the type-family sentinel so the plan YAML stays
// storable and the real secret never enters the plan stage. Fail-closed: a
// DSN shape whose password cannot be located is rejected rather than leaked.
// Returns the installed sentinel DSNs (for credential_slots exclusion) and
// user-facing fact notes.
func (s *Server) resolveProfilesInSlots(req *configbuild.SlotRequest) (profileDSNs, factsUsed []string, err error) {
	profileDSNs, factsUsed = []string{}, []string{}
	store, err := s.dsStore()
	if err != nil {
		return profileDSNs, factsUsed, fmt.Errorf("%w: 数据源存储不可用: %v", configbuild.ErrIncompleteSlots, err)
	}
	fix := func(side string, ep *configbuild.EndpointSlots) error {
		if ep == nil || ep.Profile == "" {
			return nil
		}
		typ, schema, dsn, rerr := store.Resolve(ep.Profile)
		if rerr != nil {
			return fmt.Errorf("%w: 数据源档案 %q 不可用（不存在或无法解密），请在数据源页检查后重试",
				configbuild.ErrIncompleteSlots, ep.Profile)
		}
		if ep.Type == "" {
			ep.Type = typ
		}
		if ep.Schema == "" {
			ep.Schema = schema
		}
		// 连接部件以档案为准——LLM 不得为档案端点保留自编的 host/dsn。
		ep.Host, ep.Port, ep.User, ep.Database = "", "", "", ""
		sentinel := configbuild.PasswordSentinelFor(typ)
		if sentinel == "" {
			// 无密码族（sqlite3/duckdb 文件路径）：直接使用，无泄漏面。
			ep.DSN = dsn
		} else {
			masked := config.ReplaceDSNPassword(dsn, sentinel)
			if !strings.Contains(masked, sentinel) {
				return fmt.Errorf("%w: 数据源档案 %q 的 DSN 形态无法安全脱敏；请在数据源页改用含密码的标准 DSN 后重试",
					configbuild.ErrIncompleteSlots, ep.Profile)
			}
			ep.DSN = masked
			profileDSNs = append(profileDSNs, ep.DSN)
		}
		sideLabel := "源"
		if side == "target" {
			sideLabel = "目标"
		}
		// 带上脱敏连接身份：用户一眼看出档案连的是谁（防"档案存错用户"），
		// 密码不出现在任何输出里。
		id := resolveProfileIdentity(ep.Type, ep.DSN)
		factsUsed = append(factsUsed, sideLabel+" ← 档案 "+ep.Profile+"（"+id+"）")
		return nil
	}
	if err := fix("source", &req.Source); err != nil {
		return profileDSNs, factsUsed, err
	}
	return profileDSNs, factsUsed, fix("target", req.Target)
}

// setYAMLSectionDSN rewrites the dsn value of one top-level section
// (source/target) in a plan YAML, preserving the rest of the document. Used
// at confirm time to swap a profile-referencing sentinel DSN for the real
// one decrypted from the vault.
func setYAMLSectionDSN(yamlText, section, dsn string) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(yamlText), &doc); err != nil {
		return "", fmt.Errorf("parse plan yaml: %w", err)
	}
	if len(doc.Content) == 0 {
		return "", fmt.Errorf("plan yaml is empty")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return "", fmt.Errorf("plan yaml root is not a mapping")
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i]
		val := root.Content[i+1]
		if key.Value != section || val.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(val.Content); j += 2 {
			if val.Content[j].Value == "dsn" {
				val.Content[j+1].Value = dsn
				val.Content[j+1].Tag = "!!str"
				out, err := yaml.Marshal(&doc)
				if err != nil {
					return "", err
				}
				return string(out), nil
			}
		}
		return "", fmt.Errorf("%s section has no dsn key", section)
	}
	return "", fmt.Errorf("plan yaml has no %s section", section)
}

// deriveKeywords lifts the deterministic facts out of the extracted slots
// into a space-separated lowercase keyword list for session-history
// retrieval. Deterministic: scenario, metadata source, endpoint types /
// profiles / schemas, export format, and filter table patterns — the words a
// user would search by ("oracle", "scott-xe", "csv"). No LLM involvement.
func deriveKeywords(req *configbuild.SlotRequest) []string {
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}
	if req == nil {
		return out
	}
	add(req.Scenario)
	add(req.Metadata)
	add(req.Source.Type)
	add(req.Source.Profile)
	add(req.Source.Schema)
	if req.Target != nil {
		add(req.Target.Type)
		add(req.Target.Profile)
		add(req.Target.Schema)
	}
	if req.Export != nil {
		add(req.Export.Format)
		for pat := range req.Export.Filters {
			add(pat)
		}
		for _, tb := range req.Export.Tables {
			add(tb)
		}
	}
	return out
}

// ClarifyItem is one guided follow-up: a question plus clickable options
// (chips). Empty options + AllowText means free-form input is the only way
// to answer. This is the structured half of clarify — the UI renders chips
// and a send-as-message click, grill-style.
type ClarifyItem struct {
	Question  string   `json:"question"`
	Options   []string `json:"options,omitempty"`
	AllowText bool     `json:"allow_text"`
}

// clarifyItemsFor deterministically maps missing-slot hints into guided
// questions. Known forks get fixed options; connection gaps get the saved
// datasource profiles as options (click to pick — the vault holds the
// password); anything unrecognized becomes a free-text question. No LLM
// call: mapping is pure text matching over the missing hints.
func (s *Server) clarifyItemsFor(missing []string, sess *ai.Session) []ClarifyItem {
	items := []ClarifyItem{}
	joined := strings.ToLower(strings.Join(missing, " "))
	matchedConnection := false

	for _, m := range missing {
		lm := strings.ToLower(m)
		switch {
		case strings.Contains(lm, "元数据") && strings.Contains(lm, "数据") && strings.Contains(lm, "结构"),
			strings.Contains(lm, "导出内容"):
			items = append(items, ClarifyItem{
				Question: "导出内容是什么？",
				Options:  []string{"导出表数据", "导出表结构（DDL）"},
			})
		case strings.Contains(lm, "格式") || strings.Contains(lm, "format"):
			items = append(items, ClarifyItem{
				Question: "输出格式？",
				Options:  []string{"csv", "sql", "xlsx", "tsv"},
			})
		case strings.Contains(lm, "连接") || strings.Contains(lm, "host") || strings.Contains(lm, "dsn") ||
			strings.Contains(lm, "数据库") && strings.Contains(lm, "源"):
			matchedConnection = true
		}
	}

	// 连接信息缺失 → 已存档案直接作为选项（含脱敏身份，用户看得出连的是谁）。
	if matchedConnection {
		opts := []string{}
		if dsStore, err := s.dsStore(); err == nil {
			if infos, lerr := dsStore.List(); lerr == nil {
				for _, in := range infos {
					label := in.Name + "（" + in.Type
					if in.Schema != "" {
						label += ", " + in.Schema
					}
					label += "）"
					opts = append(opts, label)
				}
			}
		}
		item := ClarifyItem{Question: "用哪个数据源？（也可选择后新建档案）", Options: opts, AllowText: true}
		items = append(items, item)
	}

	// 未识别的槽位缺口 → 自由文本问题。
	for _, m := range missing {
		lm := strings.ToLower(m)
		known := (strings.Contains(lm, "元数据") && strings.Contains(lm, "数据") && strings.Contains(lm, "结构")) ||
			strings.Contains(lm, "导出内容") || strings.Contains(lm, "格式") || strings.Contains(lm, "format") ||
			strings.Contains(lm, "连接") || strings.Contains(lm, "host") || strings.Contains(lm, "dsn") ||
			(strings.Contains(lm, "数据库") && strings.Contains(lm, "源"))
		if !known {
			items = append(items, ClarifyItem{Question: m, AllowText: true})
		}
	}

	// 语义化槽位核对单的待补项：schema 目标库等常见自由项已在上面覆盖。
	_ = joined
	if len(items) == 0 && len(missing) > 0 {
		items = append(items, ClarifyItem{Question: strings.Join(missing, "；"), AllowText: true})
	}
	return items
}
