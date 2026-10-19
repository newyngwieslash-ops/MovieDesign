package questions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/pipeline"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/tasks"
	"strings"
	"time"
)

type Option struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}
type Info struct {
	Header   string   `json:"header"`
	Question string   `json:"question"`
	Options  []Option `json:"options"`
	Multiple bool     `json:"multiple,omitempty"`
	Custom   bool     `json:"custom,omitempty"`
}
type Question struct {
	CreativeContent string            `json:"creativeContent,omitempty"`
	Creative        *pipeline.Binding `json:"creative,omitempty"`
	Rules           *RulesChange      `json:"rules,omitempty"`
	NativeSendState string            `json:"native_send_state,omitempty"`
	ID              string            `json:"id"`
	ProjectID       string            `json:"project_id"`
	RequestID       string            `json:"requestId,omitempty"`
	SessionID       string            `json:"session_id"`
	TaskID          string            `json:"task_id,omitempty"`
	Kind            string            `json:"kind"`
	Revision        int64             `json:"revision"`
	TaskRevision    int64             `json:"task_revision,omitempty"`
	InputHash       string            `json:"input_hash"`
	State           string            `json:"state"`
	Questions       []Info            `json:"questions"`
	Quote           *providers.Quote  `json:"quote,omitempty"`
	Provider        string            `json:"provider,omitempty"`
	Model           string            `json:"model,omitempty"`
	Impacts         []string          `json:"impacts,omitempty"`
	Reason          string            `json:"reason,omitempty"`
}
type HumanReply struct {
	RequestID        string     `json:"requestId"`
	Answers          [][]string `json:"answers"`
	ExpectedRevision int64      `json:"expected_revision"`
	// SessionID and Reject are trusted HTTP route scope, never body actor/channel claims.
	SessionID string `json:"-"`
	Reject    bool   `json:"-"`
	raw       json.RawMessage
}

func (i *HumanReply) UnmarshalJSON(b []byte) error {
	type plain HumanReply
	v, e := store.DecodeValue[plain](b)
	if e != nil {
		return e
	}
	*i = HumanReply(v)
	i.raw = append([]byte(nil), b...)
	return nil
}

type Receipt struct {
	ID         string     `json:"id"`
	QuestionID string     `json:"question_id"`
	RequestID  string     `json:"requestId"`
	State      string     `json:"state"`
	Answers    [][]string `json:"answers"`
	Revision   int64      `json:"revision"`
	TaskID     string     `json:"task_id,omitempty"`
}
type Service struct {
	DB     *sql.DB
	Tasks  *tasks.Service
	Events *events.Service
}

func NewService(db *sql.DB, t *tasks.Service, e *events.Service) *Service { return &Service{db, t, e} }

// LateResult is a worker SQL hook. Public Ask cannot forge a delivery-kind card.
func (s *Service) LateResult(ctx context.Context, tx *sql.Tx, p string, v tasks.TaskView, attempt int, session string) error {
	var exists, valid int
	binding := fmt.Sprintf("%s:%d", v.ID, attempt)
	if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM questions WHERE task_id=? AND kind='delivery' AND input_hash=?", v.ID, binding).Scan(&exists); e != nil {
		return e
	}
	if exists > 0 {
		return nil
	}
	if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM task_attempts a JOIN tasks t ON t.id=a.task_id WHERE a.task_id=? AND a.attempt=? AND json_array_length(a.result_json,'$.data')=json_array_length(t.frozen_input_json,'$.output.resources') AND NOT EXISTS(SELECT 1 FROM task_outputs WHERE task_id=a.task_id AND attempt=a.attempt AND state='delivered')`, v.ID, attempt).Scan(&valid); e != nil {
		return e
	}
	if valid != 1 {
		return nil
	}
	_, e := s.insert(ctx, tx, p, Question{SessionID: session, TaskID: v.ID, TaskRevision: v.Revision, Kind: "delivery", InputHash: binding, Questions: []Info{{Header: "采用取消后返回的图像", Question: "本地任务保持取消。是否收集已返回的原结果并投递到原定作品？不会重新发起生成。", Options: []Option{{Label: "采用已返回结果", Description: "使用同一任务、同一输出版本；不新增供应商请求"}}}}, Impacts: []string{"供应商可能已计费；此操作仅采用已返回结果"}})
	return e
}
func fail(status int, message string) error {
	return &store.DomainError{Status: status, Code: "invalid_input", Message: message}
}
func scope(p, id string) error {
	if e := (store.Scope{ProjectID: p, Operation: "questions", RequestID: id}).Validate(); e != nil || p == "_app" {
		return fail(400, "Invalid question scope")
	}
	return nil
}
func load(ctx context.Context, tx interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, p, id string) (Question, error) {
	var q Question
	var raw string
	e := tx.QueryRowContext(ctx, "SELECT question_json FROM questions WHERE project_id=? AND id=?", p, id).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return q, fail(404, "Question not found in this scope")
	}
	if e == nil {
		e = json.Unmarshal([]byte(raw), &q)
	}
	return q, e
}
func (s *Service) insert(ctx context.Context, tx *sql.Tx, p string, q Question) (Question, error) {
	q.ID = uuid.NewString()
	q.ProjectID = p
	q.Revision = 1
	q.State = "pending"
	raw, e := json.Marshal(q)
	if e != nil {
		return q, e
	}
	var task, revision, quote any
	if q.TaskID != "" {
		task = q.TaskID
		revision = q.TaskRevision
	}
	if q.Quote != nil {
		quote = q.Quote.QuoteID
	}
	_, e = tx.ExecContext(ctx, "INSERT INTO questions(id,project_id,session_id,task_id,kind,source,revision,task_revision,input_hash,quote_id,state,question_json,created_at)VALUES(?,?,?,?,?,'gateway',?,?,?,?,?,?,?)", q.ID, p, q.SessionID, task, q.Kind, q.Revision, revision, q.InputHash, quote, q.State, string(raw), time.Now().UnixMilli())
	if e == nil {
		e = events.Append(ctx, tx, p, q.SessionID, q.TaskID, "question_asked", raw)
	}
	return q, e
}
func currentQuote(ctx context.Context, tx *sql.Tx, p string, i tasks.Input) (providers.Quote, providers.GenerationInput, error) {
	in, e := tasks.GenerationInput(p, i)
	if e != nil {
		return providers.Quote{}, in, e
	}
	var raw, version string
	var deleted bool
	if i.Provider.Config.Kind == "llm" {
		e = tx.QueryRowContext(ctx, "SELECT config_json,credential_version FROM llm_settings WHERE id=1").Scan(&raw, &version)
	} else {
		e = tx.QueryRowContext(ctx, "SELECT config_json,credential_version,deleted FROM provider_configs WHERE id=?", i.Provider.Config.ID).Scan(&raw, &version, &deleted)
	}
	if e != nil {
		return providers.Quote{}, in, fail(409, "Provider is no longer available")
	}
	var f providers.FrozenProvider
	f.CredentialVersion = version
	if i.Provider.Config.Kind == "llm" {
		f, e = providers.TextProvider([]byte(raw), version)
	} else {
		e = json.Unmarshal([]byte(raw), &f.Config)
	}
	if e != nil {
		return providers.Quote{}, in, e
	}
	frozen, _ := json.Marshal(i.Provider)
	current, _ := json.Marshal(f)
	if deleted || !f.Config.Enabled || !bytes.Equal(frozen, current) {
		return providers.Quote{}, in, fail(409, "Provider configuration or credential changed; create a new generation intent")
	}
	q, e := providers.Estimate(in, i.Provider)
	return q, in, e
}

// Billing is a task transaction hook; it performs SQL/pure validation only.
func (s *Service) Billing(ctx context.Context, tx *sql.Tx, p string, v *tasks.TaskView, i tasks.Input, fresh bool) error {
	q, in, e := currentQuote(ctx, tx, p, i)
	if e != nil {
		return e
	}
	if !fresh && i.QuoteID != "" {
		var hash, version, raw string
		var expiry int64
		e = tx.QueryRowContext(ctx, "SELECT input_hash,version,expires_at,frozen_json FROM price_quotes WHERE id=? AND project_id=?", i.QuoteID, p).Scan(&hash, &version, &expiry, &raw)
		if e != nil {
			return e
		}
		var saved providers.QuotedInput
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.UseNumber()
		if e = dec.Decode(&saved); e != nil {
			return e
		}
		if hash != q.InputHash || version != q.Version || expiry <= time.Now().UnixMilli() {
			return fail(409, "Quote expired or changed")
		}
		q = saved.Quote
	} else {
		raw, _ := json.Marshal(providers.QuotedInput{Quote: q, Input: in, Provider: i.Provider})
		if _, e = tx.ExecContext(ctx, "INSERT INTO price_quotes(id,project_id,input_hash,frozen_json,source,version,expires_at,created_at)VALUES(?,?,?,?,?,?,?,?)", q.QuoteID, p, q.InputHash, string(raw), q.Source, q.Version, q.ExpiresAt, time.Now().UnixMilli()); e != nil {
			return e
		}
	}
	v.Billing.EstimatedCredits = &q.EstimatedCredits
	_, e = s.insert(ctx, tx, p, Question{SessionID: i.SessionID, TaskID: v.ID, Kind: "billing", TaskRevision: v.Revision, InputHash: q.InputHash, Quote: &q, Provider: i.Provider.Config.Name, Model: i.Provider.Config.ModelID, Questions: []Info{{Header: "生成费用确认", Question: "确认此报价并提交一次生成？", Options: []Option{{Label: "确认生成", Description: "批准一次供应商请求；重复回执不会再次生成"}}}}, Impacts: []string{"提交一次生成请求，供应商可能计费", "创建声明的作品输出；取消不能撤回已发送的请求"}})
	return e
}
func validate(q Question) error {
	if q.Kind != "choice" && q.Kind != "storyboard" {
		return fail(400, "Unsupported ordinary question kind")
	}
	if q.SessionID == "" || scope("session", q.SessionID) != nil || len(q.Questions) < 1 || len(q.Questions) > 8 {
		return fail(400, "Question requires a session and 1-8 prompts")
	}
	for _, info := range q.Questions {
		if strings.TrimSpace(info.Header) == "" || len(info.Header) > 120 || strings.TrimSpace(info.Question) == "" || len(info.Question) > 8192 || len(info.Options) > 20 || len(info.Options) == 0 && !info.Custom || q.Kind == "storyboard" && info.Custom {
			return fail(400, "Invalid question text or options")
		}
		seen := map[string]bool{}
		for _, o := range info.Options {
			if strings.TrimSpace(o.Label) == "" || len(o.Label) > 200 || len(o.Description) > 2048 || seen[o.Label] {
				return fail(400, "Invalid or duplicate option")
			}
			seen[o.Label] = true
		}
	}
	return nil
}

// Ask accepts ordinary structured decisions or an explicit billing renewal bound
// to an existing waiting task. Billing content/price is always generated here.
func (s *Service) Ask(ctx context.Context, p string, q Question) (Question, error) {
	var result Question
	if q.Creative != nil {
		if q.Kind != "creative" || q.TaskID != "" || len(q.Questions) != 0 || q.Rules != nil || q.Quote != nil {
			return result, fail(400, "Creative proposal accepts binding only")
		}
		return s.ProposeCreative(ctx, p, q.SessionID, q.RequestID, *q.Creative)
	}
	if q.Rules != nil {
		return result, fail(400, "Use typed rule proposal")
	}
	if e := scope(p, q.RequestID); e != nil {
		return result, e
	}
	raw, _ := json.Marshal(q)
	out, e := store.WriteRequest(ctx, s.DB, store.Scope{ProjectID: p, Operation: "question.ask", RequestID: q.RequestID}, raw, func(tx *sql.Tx) ([]byte, error) {
		if q.Kind == "billing" {
			if q.TaskID == "" || q.TaskRevision < 1 || len(q.Questions) != 0 || q.Quote != nil {
				return nil, fail(400, "Billing renewal requires task/session/revision only")
			}
			i, e := s.Tasks.BillingInput(ctx, tx, p, q.SessionID, q.TaskID, q.TaskRevision)
			if e != nil {
				return nil, e
			}
			var active int
			if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM questions q JOIN price_quotes p ON p.id=q.quote_id WHERE q.task_id=? AND q.task_revision=? AND q.state='pending' AND p.expires_at>?", q.TaskID, q.TaskRevision, time.Now().UnixMilli()).Scan(&active); e != nil {
				return nil, e
			}
			if active != 0 {
				return nil, fail(409, "An unexpired billing card already exists")
			}
			if _, e = tx.ExecContext(ctx, "UPDATE questions SET state='superseded',revision=revision+1,question_json=json_set(question_json,'$.state','superseded','$.revision',revision+1,'$.reason','报价已被新卡替代') WHERE task_id=? AND state='pending'", q.TaskID); e != nil {
				return nil, e
			}
			v := tasks.TaskView{ID: q.TaskID, Revision: q.TaskRevision}
			if e = s.Billing(ctx, tx, p, &v, i, true); e != nil {
				return nil, e
			}
			var saved string
			if e = tx.QueryRowContext(ctx, "SELECT question_json FROM questions WHERE task_id=? AND state='pending' ORDER BY created_at DESC,rowid DESC LIMIT 1", q.TaskID).Scan(&saved); e != nil {
				return nil, e
			}
			return []byte(saved), nil
		}
		if e := validate(q); e != nil {
			return nil, e
		}
		if q.TaskID != "" {
			var session string
			var rev int64
			if e := tx.QueryRowContext(ctx, "SELECT session_id,revision FROM tasks WHERE id=? AND project_id=?", q.TaskID, p).Scan(&session, &rev); e != nil || session != q.SessionID || rev != q.TaskRevision {
				return nil, fail(409, "Question task scope/revision changed")
			}
		}
		hash := sha256.Sum256(raw)
		q.InputHash = hex.EncodeToString(hash[:])
		saved, e := s.insert(ctx, tx, p, q)
		if e != nil {
			return nil, e
		}
		return json.Marshal(saved)
	})
	if e == nil {
		e = json.Unmarshal(out, &result)
		s.Events.Notify()
	}
	return result, e
}
func (s *Service) List(ctx context.Context, p, session string) ([]Question, error) {
	if e := scope(p, "list"); e != nil {
		return nil, e
	}
	if session != "" && scope("session", session) != nil {
		return nil, fail(400, "Invalid session")
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT q.question_json,coalesce(p.expires_at,0),coalesce(t.revision,0),coalesce(n.send_state,'') FROM questions q LEFT JOIN native_questions n ON n.question_id=q.id LEFT JOIN price_quotes p ON p.id=q.quote_id LEFT JOIN tasks t ON t.id=q.task_id WHERE q.project_id=? AND (?='' OR q.session_id=?) ORDER BY q.created_at,q.rowid`, p, session, session)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	// ponytail: retain and list local decision history in memory; paginate when project histories grow.
	out := []Question{}
	for rows.Next() {
		var q Question
		var raw, nativeState string
		var expiry, revision int64
		if e = rows.Scan(&raw, &expiry, &revision, &nativeState); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(raw), &q); e != nil {
			return nil, e
		}
		q.NativeSendState = nativeState
		if q.State == "pending" {
			if q.TaskID != "" && revision != q.TaskRevision {
				q.State = "stale"
				q.Reason = "任务已变化"
			} else if q.Quote != nil && expiry <= time.Now().UnixMilli() {
				q.State = "expired"
				q.Reason = "报价已过期，请明确继续后重新报价"
			}
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
func validateAnswers(q Question, i HumanReply) error {
	if i.Answers == nil {
		return fail(400, "answers must be an array")
	}
	if i.Reject {
		if len(i.Answers) != 0 {
			return fail(400, "Rejection requires empty answers")
		}
		return nil
	}
	if len(i.Answers) != len(q.Questions) {
		return fail(400, "One answer array is required for each question")
	}
	for n, info := range q.Questions {
		a := i.Answers[n]
		if len(a) < 1 || len(a) > 20 || !info.Multiple && len(a) != 1 {
			return fail(400, "Invalid answer cardinality")
		}
		seen := map[string]bool{}
		for _, v := range a {
			found := false
			for _, o := range info.Options {
				if o.Label == v {
					found = true
				}
			}
			if strings.TrimSpace(v) == "" || len(v) > 4096 || seen[v] || !found && (!info.Custom || q.Kind != "choice") {
				return fail(400, "Answer must match an explicit option")
			}
			seen[v] = true
		}
	}
	return nil
}
func (s *Service) Reply(ctx context.Context, p, id string, i HumanReply) (Receipt, error) {
	var receipt Receipt
	if e := scope(p, id); e != nil {
		return receipt, e
	}
	if e := scope(p, i.RequestID); e != nil {
		return receipt, e
	}
	if i.ExpectedRevision < 1 || i.SessionID != "" && scope("session", i.SessionID) != nil {
		return receipt, fail(400, "Invalid reply scope/revision")
	}
	raw, _ := json.Marshal(i)
	if i.raw != nil {
		var original HumanReply
		if e := json.Unmarshal(i.raw, &original); e != nil {
			return receipt, e
		}
		expected, _ := json.Marshal(original)
		if !bytes.Equal(raw, expected) {
			return receipt, fail(400, "Reply input changed")
		}
		raw = i.raw
	}
	// Include trusted route values in receipt identity; the same key cannot change scope/action.
	wire, _ := json.Marshal(struct {
		Body    json.RawMessage `json:"body"`
		Session string          `json:"session"`
		Reject  bool            `json:"reject"`
	}{raw, i.SessionID, i.Reject})
	out, e := store.WriteRequest(context.WithoutCancel(ctx), s.DB, store.Scope{ProjectID: p, Operation: "question.reply." + id, RequestID: i.RequestID}, wire, func(tx *sql.Tx) ([]byte, error) {
		ctx = context.WithoutCancel(ctx)
		q, e := load(ctx, tx, p, id)
		if e != nil {
			return nil, e
		}
		if q.SessionID != i.SessionID {
			return nil, fail(404, "Question not found in this session")
		}
		if q.State != "pending" || q.Revision != i.ExpectedRevision {
			return nil, fail(409, "Question has changed")
		}
		if e = validateAnswers(q, i); e != nil {
			return nil, e
		}
		if q.Kind == "billing" && !i.Reject {
			input, e := s.Tasks.BillingInput(ctx, tx, p, q.SessionID, q.TaskID, q.TaskRevision)
			if e != nil {
				return nil, e
			}
			if !i.Reject {
				current, _, e := currentQuote(ctx, tx, p, input)
				if e != nil {
					return nil, e
				}
				var hash, version string
				var expiry int64
				if q.Quote == nil {
					return nil, fail(409, "Missing quote")
				}
				if e = tx.QueryRowContext(ctx, "SELECT input_hash,version,expires_at FROM price_quotes WHERE id=? AND project_id=?", q.Quote.QuoteID, p).Scan(&hash, &version, &expiry); e != nil {
					return nil, e
				}
				if expiry <= time.Now().UnixMilli() || hash != q.InputHash || hash != current.InputHash || version != q.Quote.Version || version != current.Version {
					return nil, fail(409, "Quote expired or input/pricing changed")
				}
			}
		}
		if q.TaskID != "" {
			if q.Kind == "delivery" {
				e = s.Tasks.AdoptQuestion(ctx, tx, p, q.SessionID, q.TaskID, q.TaskRevision, q.InputHash, i.Reject)
			} else {
				e = s.Tasks.DecideQuestion(ctx, tx, p, q.SessionID, q.TaskID, q.TaskRevision, q.Kind == "billing", i.Reject)
			}
			if e != nil {
				return nil, e
			}
		}
		q.Revision++
		q.State = "answered"
		if q.Kind == "billing" || q.Kind == "delivery" || q.Kind == "creative" {
			q.State = "approved"
		}
		if i.Reject {
			q.State = "rejected"
		}
		saved, _ := json.Marshal(q)
		if _, e = tx.ExecContext(ctx, "UPDATE questions SET state=?,revision=?,question_json=? WHERE id=?", q.State, q.Revision, string(saved), id); e != nil {
			return nil, e
		}
		receipt = Receipt{ID: uuid.NewString(), QuestionID: id, RequestID: i.RequestID, State: q.State, Answers: i.Answers, Revision: q.Revision, TaskID: q.TaskID}
		if q.Kind == "project_rules" && !i.Reject {
			if q.Rules == nil {
				return nil, fail(409, "Rule proposal missing")
			}
			if e = checkRules(ctx, tx, p, *q.Rules); e != nil {
				return nil, e
			}
			if _, e = tx.ExecContext(ctx, "INSERT INTO project_rules(project_id,content,receipt_id)VALUES(?,?,?) ON CONFLICT(project_id) DO UPDATE SET content=excluded.content,receipt_id=excluded.receipt_id", p, q.Rules.Content, receipt.ID); e != nil {
				return nil, e
			}
		}
		b, _ := json.Marshal(receipt)
		if _, e = tx.ExecContext(ctx, "INSERT INTO question_receipts(id,question_id,request_id,actor,response_json,created_at)VALUES(?,?,?,'human',?,?)", receipt.ID, id, i.RequestID, string(b), time.Now().UnixMilli()); e != nil {
			return nil, e
		}
		if q.Kind == "creative" && !i.Reject {
			if q.Creative == nil {
				return nil, fail(409, "Creative binding missing")
			}
			if _, e = pipeline.ApproveStoryboard(ctx, tx, pipeline.HumanReceipt{ProjectID: p, QuestionID: id, ReceiptID: receipt.ID, Binding: *q.Creative}); e != nil {
				return nil, e
			}
		}
		if e = events.Append(ctx, tx, p, q.SessionID, q.TaskID, "question_resolved", saved); e != nil {
			return nil, e
		}
		return b, nil
	})
	if e == nil {
		e = json.Unmarshal(out, &receipt)
		s.Events.Notify()
		if e == nil && receipt.State == "approved" && receipt.TaskID != "" {
			_ = s.Tasks.Dispatch(p, receipt.TaskID)
		}
	}
	return receipt, e
}
