package agent

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
)

// Only a contiguous, understood text prefix is exposed. The first unsupported,
// mixed, tool or terminal record and its entire suffix stay in their original
// order until completedInference and the admission commit permit tool effects.
// ponytail: 32MiB/10min total, at most65536 early records and1MiB per early
// record. Unrecognized variants retain bounded buffering; extend this allowlist
// only with the bundled provider's protocol and a runnable native fixture.
func (r *Relay) readInference(ctx context.Context, w http.ResponseWriter, body io.Reader, s ToolScope, step int, operation string) ([]byte, int, bool, error) {
	reader := bufio.NewReader(io.LimitReader(body, inferenceLimit+1))
	var all, frame bytes.Buffer
	prefix, seq := 0, 0
	started, buffered := false, false
	var sensitiveTail string
	for {
		if err := ctx.Err(); err != nil {
			return nil, prefix, started, err
		}
		line, readErr := reader.ReadBytes('\n')
		all.Write(line)
		if all.Len() > inferenceLimit {
			return nil, prefix, started, errors.New("inference stream limit")
		}
		if !buffered {
			frame.Write(line)
		}
		if !buffered && len(bytes.TrimRight(line, "\r\n")) == 0 && len(line) > 0 {
			b := frame.Bytes()
			text, safe := safeInferenceFrame(operation, b)
			// Decode JSON string escapes too: a supplier may reflect its header
			// credential in a harmless-looking metadata string or text delta.
			var jsonLines []string
			for _, line := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(line, "data:") {
					jsonLines = append(jsonLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
				}
			}
			// Never persist reflected frozen credentials, including a credential
			// split across text deltas. No request headers enter this journal.
			for _, secret := range []string{r.key, r.token} {
				if secret != "" && bytes.Contains(b, []byte(secret)) {
					return nil, prefix, started, errors.New("inference reflected private credential")
				}
			}
			nextTail, sensitive := inferenceCredential([]byte(strings.Join(jsonLines, "\n")), text, sensitiveTail, r.key, r.token)
			if sensitive {
				return nil, prefix, started, errors.New("inference reflected private credential")
			}
			if !safe || len(b) > 1<<20 {
				buffered = true
			} else {
				seq++
				if seq > 65536 {
					return nil, prefix, started, errors.New("inference record limit")
				}
				scope := "chat"
				if s.Epoch == "text" {
					scope = "text"
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, err := r.DB.ExecContext(ctx, "INSERT INTO inference_stream(scope,turn_id,step,seq,project_id,session_id,frame,text_delta,created_at)VALUES(?,?,?,?,?,?,?,?,?)", scope, s.TurnID, step, seq, s.ProjectID, s.SessionID, b, text, time.Now().UnixMilli())
				cancel()
				if err != nil {
					return nil, prefix, started, err
				}
				if !started {
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(200)
					started = true
				}
				if err = writeInference(w, b); err != nil {
					return nil, prefix, started, err
				}
				prefix = all.Len()
				sensitiveTail = nextTail
			}
			frame.Reset()
		}
		if readErr != nil {
			if readErr != io.EOF {
				return nil, prefix, started, readErr
			}
			return all.Bytes(), prefix, started, nil
		}
	}
}

// Inspect only exact frozen secrets, including JSON-escaped values and a secret
// split across real text deltas. This rejects a response; it never rewrites one.
func inferenceCredential(payload []byte, text, tail string, secrets ...string) (string, bool) {
	// Token returns actual decoded strings, including object keys. Serializing
	// them again would escape &, <, >, quotes and backslashes and hide a match.
	decoder := json.NewDecoder(bytes.NewReader(payload))
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		if value, ok := token.(string); ok {
			for _, secret := range secrets {
				if secret != "" && strings.Contains(value, secret) {
					return "", true
				}
			}
		}
	}
	joined := tail + text
	keep := 0
	for _, secret := range secrets {
		keep = max(keep, len(secret))
		if secret != "" && strings.Contains(joined, secret) {
			return "", true
		}
	}
	if len(joined) > keep {
		joined = joined[len(joined)-keep:]
	}
	return joined, false
}

func writeInference(w http.ResponseWriter, data []byte) error {
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return err
	}
	if err = controller.Flush(); errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

// SSE framing is read without rewriting it. Multiple data lines are interpreted
// exactly as SSE (newline joined); unknown fields stop early forwarding.
func safeInferenceFrame(operation string, frame []byte) (string, bool) {
	var payload []string
	eventName := ""
	lines := strings.ReplaceAll(strings.ReplaceAll(string(frame), "\r\n", "\n"), "\r", "\n")
	for _, line := range strings.Split(lines, "\n") {
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "data":
			payload = append(payload, value)
		case "event":
			if eventName != "" {
				return "", false
			}
			eventName = value
		default:
			return "", false
		}
	}
	var object map[string]json.RawMessage
	if len(payload) == 0 || json.Unmarshal([]byte(strings.Join(payload, "\n")), &object) != nil || object == nil {
		return "", false
	}
	if operation == "chat/completions" {
		if eventName != "" && eventName != "message" {
			return "", false
		}
		if !onlyFields(object, "id", "object", "created", "model", "system_fingerprint", "choices", "usage", "service_tier") || !nullField(object["usage"]) {
			return "", false
		}
		var choices []map[string]json.RawMessage
		if json.Unmarshal(object["choices"], &choices) != nil || len(choices) != 1 {
			return "", false
		}
		choice := choices[0]
		if !onlyFields(choice, "index", "delta", "finish_reason", "logprobs") || !nullField(choice["finish_reason"]) || !nullField(choice["logprobs"]) {
			return "", false
		}
		var index int
		if b := choice["index"]; len(b) > 0 && (json.Unmarshal(b, &index) != nil || index != 0) {
			return "", false
		}
		var delta map[string]json.RawMessage
		if json.Unmarshal(choice["delta"], &delta) != nil || delta == nil || !onlyFields(delta, "role", "content") {
			return "", false
		}
		if b := delta["role"]; len(b) > 0 && !stringField(b, "assistant") {
			return "", false
		}
		if nullField(delta["content"]) {
			return "", true
		}
		var text string
		return textValue(delta["content"], &text)
	}
	var kind string
	if json.Unmarshal(object["type"], &kind) != nil || eventName != "" && eventName != kind {
		return "", false
	}
	if operation == "responses" {
		switch kind {
		case "response.created", "response.in_progress":
			if !onlyFields(object, "type", "sequence_number", "response") {
				return "", false
			}
			var response map[string]json.RawMessage
			if json.Unmarshal(object["response"], &response) != nil || !onlyFields(response, "id", "object", "created_at", "status", "model", "output", "error", "incomplete_details", "usage") || !stringField(response["status"], "in_progress") || !emptyArray(response["output"]) || !nullField(response["error"]) || !nullField(response["incomplete_details"]) {
				return "", false
			}
			return "", true
		case "response.output_item.added":
			if !onlyFields(object, "type", "sequence_number", "output_index", "item") {
				return "", false
			}
			var item map[string]json.RawMessage
			if json.Unmarshal(object["item"], &item) != nil || !onlyFields(item, "id", "type", "role", "status", "content") || !stringField(item["type"], "message") || !stringField(item["role"], "assistant") || !emptyArray(item["content"]) || len(item["status"]) > 0 && !stringField(item["status"], "in_progress") {
				return "", false
			}
			return "", true
		case "response.content_part.added":
			if !onlyFields(object, "type", "sequence_number", "item_id", "output_index", "content_index", "part") {
				return "", false
			}
			var part map[string]json.RawMessage
			if json.Unmarshal(object["part"], &part) != nil || !onlyFields(part, "type", "text", "annotations", "logprobs") || !stringField(part["type"], "output_text") || !emptyArray(part["annotations"]) || !emptyArray(part["logprobs"]) {
				return "", false
			}
			var text string
			if _, ok := textValue(part["text"], &text); !ok || text != "" {
				return "", false
			}
			return "", true
		case "response.output_text.delta":
			if !onlyFields(object, "type", "sequence_number", "item_id", "output_index", "content_index", "delta", "logprobs") || !emptyArray(object["logprobs"]) {
				return "", false
			}
			var text string
			return textValue(object["delta"], &text)
		}
	}
	if operation == "messages" {
		switch kind {
		case "message_start":
			if !onlyFields(object, "type", "message") {
				return "", false
			}
			var message map[string]json.RawMessage
			if json.Unmarshal(object["message"], &message) != nil || !onlyFields(message, "id", "type", "role", "model", "content", "stop_reason", "stop_sequence", "usage") || !stringField(message["type"], "message") || !stringField(message["role"], "assistant") || !emptyArray(message["content"]) || !nullField(message["stop_reason"]) || !nullField(message["stop_sequence"]) {
				return "", false
			}
			return "", true
		case "content_block_start":
			if !onlyFields(object, "type", "index", "content_block") {
				return "", false
			}
			var block map[string]json.RawMessage
			if json.Unmarshal(object["content_block"], &block) != nil || !onlyFields(block, "type", "text") || !stringField(block["type"], "text") {
				return "", false
			}
			var text string
			return textValue(block["text"], &text)
		case "content_block_delta":
			if !onlyFields(object, "type", "index", "delta") {
				return "", false
			}
			var delta map[string]json.RawMessage
			if json.Unmarshal(object["delta"], &delta) != nil || !onlyFields(delta, "type", "text") || !stringField(delta["type"], "text_delta") {
				return "", false
			}
			var text string
			return textValue(delta["text"], &text)
		}
	}
	return "", false
}

func onlyFields(object map[string]json.RawMessage, fields ...string) bool {
	for key := range object {
		found := false
		for _, field := range fields {
			if key == field {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func nullField(b json.RawMessage) bool { return len(b) == 0 || string(b) == "null" }
func stringField(b json.RawMessage, expected string) bool {
	var value string
	return json.Unmarshal(b, &value) == nil && value == expected
}
func emptyArray(b json.RawMessage) bool {
	var a []json.RawMessage
	return nullField(b) || json.Unmarshal(b, &a) == nil && len(a) == 0
}
func textValue(b json.RawMessage, text *string) (string, bool) {
	err := json.Unmarshal(b, text)
	return *text, err == nil
}

// Recover only the missing suffix of text already delivered by this admission.
// Native snapshots remain the first source. Exact prefix comparison avoids
// repeating a shorter native snapshot; a disagreement never invents a merge.
func restoreInferencePartial(ctx context.Context, tx *sql.Tx, messageID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT j.turn_id,j.step,j.text_delta,s.project_id,s.id,d.message_id
	 FROM inference_stream j JOIN chat_dispatch d ON d.turn_id=j.turn_id JOIN chat_sessions s ON s.id=(SELECT session_id FROM chat_messages WHERE id=d.message_id)
	 JOIN inference_requests r ON r.turn_id=j.turn_id AND r.step=j.step
	 WHERE j.scope='chat' AND j.project_id=s.project_id AND j.session_id=s.id AND d.message_id=? AND r.state IN ('forwarding','unknown') ORDER BY j.step,j.seq`, messageID)
	if err != nil {
		return err
	}
	type partial struct {
		turn, project, session, message string
		step                            int
		text                            strings.Builder
	}
	var parts []*partial
	for rows.Next() {
		var turn, text, project, session, message string
		var step int
		if err = rows.Scan(&turn, &step, &text, &project, &session, &message); err != nil {
			break
		}
		if len(parts) == 0 || parts[len(parts)-1].step != step {
			parts = append(parts, &partial{turn: turn, project: project, session: session, message: message, step: step})
		}
		parts[len(parts)-1].text.WriteString(text)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, part := range parts {
		text := part.text.String()
		if text == "" {
			continue
		}
		native, err := tx.QueryContext(ctx, `SELECT message_id,part_id,public_json FROM chat_parts WHERE session_id=? AND json_extract(public_json,'$.message_id')=? AND json_extract(public_json,'$.step')=? AND json_extract(public_json,'$.part_type')='text' ORDER BY rowid`, part.session, part.message, part.step)
		if err != nil {
			return err
		}
		var known strings.Builder
		type savedPart struct {
			message, id string
			frame       ChatMessageEnvelope
		}
		var saved []savedPart
		for native.Next() {
			var raw string
			var existing savedPart
			if err = native.Scan(&existing.message, &existing.id, &raw); err != nil {
				break
			}
			if err = json.Unmarshal([]byte(raw), &existing.frame); err != nil {
				break
			}
			known.WriteString(existing.frame.Content)
			saved = append(saved, existing)
		}
		if err == nil {
			err = native.Err()
		}
		native.Close()
		if err != nil {
			return err
		}
		if strings.HasPrefix(known.String(), text) {
			continue
		}
		if len(saved) > 0 {
			if strings.HasPrefix(text, known.String()) {
				last := &saved[len(saved)-1]
				last.frame.ContentDelta = text[known.Len():]
				last.frame.Content += last.frame.ContentDelta
				last.frame.Type = "content_delta"
				saved = saved[len(saved)-1:]
			} else {
				// The journal is the exact delivered supplier prefix. Preserve the
				// differing raw native snapshots for audit, but replace public text
				// with that prefix rather than guessing a merge or displaying twice.
				for i := range saved {
					saved[i].frame.Type = "content_replace"
					saved[i].frame.Content = ""
					saved[i].frame.ContentDelta = ""
				}
				saved[0].frame.Content = text
			}
			for _, existing := range saved {
				frame := existing.frame
				frame.State = "unknown"
				history := frame
				history.ContentDelta = ""
				public, _ := json.Marshal(history)
				if _, err = tx.ExecContext(ctx, "UPDATE chat_parts SET public_json=? WHERE session_id=? AND message_id=? AND part_id=?", string(public), part.session, existing.message, existing.id); err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `UPDATE chat_messages SET content_json=json_object('reply_to',?,'parts',json((SELECT json_group_array(json(public_json)) FROM chat_parts WHERE session_id=? AND message_id=?))) WHERE id=? AND session_id=?`, part.message, part.session, existing.message, "assistant_"+existing.message, part.session); err != nil {
					return err
				}
				if frame.Type == "content_delta" {
					frame.Content = ""
				}
				payload, _ := json.Marshal(frame)
				if err = events.Append(ctx, tx, part.project, part.session, "", "chat_message", payload); err != nil {
					return err
				}
			}
			continue
		}
		id := fmt.Sprintf("relay_%s_%d", part.turn, part.step)
		frame := ChatMessageEnvelope{SessionID: part.session, MessageID: part.message, PartID: id, Step: part.step, Type: "content_delta", PartType: "text", State: "unknown", ContentDelta: text[known.Len():]}
		history := frame
		history.Content = frame.ContentDelta
		history.ContentDelta = ""
		public, _ := json.Marshal(history)
		if _, err = tx.ExecContext(ctx, "INSERT INTO chat_parts(session_id,message_id,part_id,snapshot_json,public_json)VALUES(?,?,?,'{}',?)", part.session, id, id, string(public)); err != nil {
			return err
		}
		content, _ := json.Marshal(map[string]any{"reply_to": part.message, "parts": []ChatMessageEnvelope{history}})
		if _, err = tx.ExecContext(ctx, "INSERT INTO chat_messages(id,session_id,request_id,role,content_json,created_at)VALUES(?,?,?,'assistant',?,?)", "assistant_"+id, part.session, "assistant_"+id, string(content), time.Now().UnixMilli()); err != nil {
			return err
		}
		payload, _ := json.Marshal(frame)
		if err = events.Append(ctx, tx, part.project, part.session, "", "chat_message", payload); err != nil {
			return err
		}
	}
	return nil
}

func (c *Chat) restoreStreamPartials() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT message_id FROM chat_dispatch WHERE state='unknown'")
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = restoreInferencePartial(ctx, tx, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
