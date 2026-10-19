package questions

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/pipeline"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

// ProposeCreative publishes only a candidate; the sole human Reply route approves.
func (s *Service) ProposeCreative(ctx context.Context, p, session, key string, b pipeline.Binding) (Question, error) {
	var result Question
	if scope(p, key) != nil || scope("session", session) != nil {
		return result, fail(400, "Invalid creative proposal scope")
	}
	raw, _ := json.Marshal(struct {
		Session string
		Binding pipeline.Binding
	}{session, b})
	out, e := store.WriteRequest(ctx, s.DB, store.Scope{ProjectID: p, Operation: "creative.propose", RequestID: key}, raw, func(tx *sql.Tx) ([]byte, error) {
		if e := pipeline.CheckBinding(ctx, tx, p, b); e != nil {
			return nil, e
		}
		preview, e := pipeline.Preview(ctx, tx, b)
		if e != nil {
			return nil, e
		}
		label := map[string]string{"intent": "意图", "script": "脚本", "storyboard": "分镜"}[b.Gate]
		q, e := s.insert(ctx, tx, p, Question{RequestID: key, SessionID: session, Kind: "creative", Creative: &b, CreativeContent: preview, InputHash: b.SemanticHash, Questions: []Info{{Header: "批准" + label, Question: fmt.Sprintf("批准%s revision %d？请审阅完整文档 Version %s。此批准仅适用于显示的版本与语义哈希。", label, b.Revision, b.DocumentVersionID), Options: []Option{{Label: "批准" + label, Description: "保存此版本的人类批准；不提交付费生成"}}}}, Impacts: []string{"上游或当前内容变化后需要重新批准；已提交任务保留冻结版本，请明确决定是否取消"}})
		if e != nil {
			return nil, e
		}
		return json.Marshal(q)
	})
	if e == nil {
		e = json.Unmarshal(out, &result)
		s.Events.Notify()
	}
	return result, e
}
