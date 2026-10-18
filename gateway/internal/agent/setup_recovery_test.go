package agent

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

type setupTransport func(*http.Request) (*http.Response, error)

func (f setupTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A known-unsent failure must keep a cleanup owner, never replay its prompt,
// and never let another project borrow the failed setup's epoch.
func TestChatPrePromptFailureRetainsCleanup(t *testing.T) {
	for _, stage := range []string{"disconnect", "epoch", "connect", "connect-lost", "association"} {
		t.Run(stage, func(t *testing.T) {
			f := newChatFixture(t, 0, false)
			var failed, cleaned atomic.Bool
			release := make(chan struct{})
			blocked := make(chan ToolScope, 1)
			f.control = func(path string) bool {
				return (stage == "disconnect" && path == "/mcp/design/disconnect" || stage == "connect" && path == "/mcp/design/connect") && failed.CompareAndSwap(false, true)
			}
			if stage == "connect-lost" {
				transport := f.chat.Host.client.Transport
				f.chat.Host.client.Transport = setupTransport(func(r *http.Request) (*http.Response, error) {
					response, e := transport.RoundTrip(r)
					if e == nil && r.URL.Path == "/mcp/design/connect" && failed.CompareAndSwap(false, true) {
						response.Body.Close()
						return nil, errors.New("connection applied; response lost")
					}
					return response, e
				})
			}
			if stage == "association" {
				if _, e := f.chat.DB.Exec(`CREATE TRIGGER setup_fault BEFORE UPDATE OF turn_id ON chat_dispatch WHEN NEW.turn_id IS NOT NULL BEGIN SELECT RAISE(ABORT,'association failure'); END`); e != nil {
					t.Fatal(e)
				}
			}
			f.chat.CloseEpoch = func(epoch string) error {
				if epoch == "" {
					if stage == "epoch" && failed.CompareAndSwap(false, true) {
						return errors.New("initial epoch close failed")
					}
					return nil
				}
				select {
				case <-release:
					cleaned.Store(true)
					return nil
				default:
				}
				f.chat.Turns.mu.Lock()
				owned := f.chat.Turns.active
				f.chat.Turns.mu.Unlock()
				select {
				case blocked <- owned:
				default:
				}
				return errors.New("owned cleanup still blocked")
			}
			m, e := f.chat.Send(context.Background(), f.project, f.session, f.input("first"))
			if e != nil {
				t.Fatal(e)
			}
			other := queueOtherProject(t, f)
			if _, e = f.chat.Cancel(context.Background(), f.project, f.session, []byte(`{"requestId":"cancel-unsent"}`)); e != nil {
				t.Fatal(e)
			}
			var old ToolScope
			select {
			case old = <-blocked:
			case <-time.After(2 * time.Second):
				t.Fatal("pre-prompt failure abandoned its owned cleanup")
			}
			if f.prompts.Load() != 0 || cleaned.Load() {
				t.Fatal("prompt submitted before isolation recovered")
			}
			if _, e = f.chat.Turns.Begin(context.Background(), other, "blocked", "ses_blocked"); !errors.Is(e, ErrTurnBusy) {
				t.Fatal("failed scope released early", e)
			}
			var terminal, inferences int
			if e = f.chat.DB.QueryRow("SELECT terminal FROM chat_dispatch WHERE message_id=?", m.ID).Scan(&terminal); e != nil || terminal != 1 {
				t.Fatal("known-unsent message not settled", terminal, e)
			}
			f.chat.DB.QueryRow("SELECT count(*) FROM inference_requests").Scan(&inferences)
			if inferences != 0 {
				t.Fatal("unsent prompt admitted inference")
			}
			if stage == "association" {
				if _, e = f.chat.DB.Exec("DROP TRIGGER setup_fault"); e != nil {
					t.Fatal(e)
				}
			}
			close(release)
			waitChat(t, func() bool { return f.prompts.Load() == 1 })
			if !cleaned.Load() || f.chat.Turns.Current().ProjectID != other {
				t.Fatal("next project started before cleanup")
			}
			if done, e := f.chat.Turns.Acquire(old); e == nil {
				done()
				t.Fatal("old scope acquired")
			}
			assertOneTerminal(t, f, m.ID)
			if _, e = f.chat.Send(context.Background(), f.project, f.session, f.input("first")); e != nil {
				t.Fatal(e)
			}
			if f.prompts.Load() != 1 {
				t.Fatal("failed original prompt replayed")
			}
		})
	}
}

func TestChatPrePromptCleanupShutdownRestartNeverResubmits(t *testing.T) {
	f := newChatFixture(t, 0, false)
	var failed atomic.Bool
	f.control = func(path string) bool { return path == "/mcp/design/connect" && failed.CompareAndSwap(false, true) }
	blocked := make(chan struct{}, 1)
	f.chat.CloseEpoch = func(epoch string) error {
		if epoch != "" {
			select {
			case blocked <- struct{}{}:
			default:
			}
			return errors.New("cleanup unavailable")
		}
		return nil
	}
	m, e := f.chat.Send(context.Background(), f.project, f.session, f.input("first"))
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("no retained cleanup")
	}
	closed := make(chan struct{})
	go func() { f.chat.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown hung in cleanup")
	}
	turns := NewTurns(f.chat.DB)
	if e = turns.Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	next := NewChat(f.chat.DB, turns, f.chat.Events, f.chat.Questions)
	next.Grace = 40 * time.Millisecond
	if e = next.Start(f.chat.Host, func(string) error { return nil }); e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	if _, e = next.Send(context.Background(), f.project, f.session, f.input("first")); e != nil {
		t.Fatal(e)
	}
	if f.prompts.Load() != 0 {
		t.Fatal("restart submitted failed original")
	}
	f.chat = next
	other := queueOtherProject(t, f)
	waitChat(t, func() bool { return f.prompts.Load() == 1 })
	if turns.Current().ProjectID != other {
		t.Fatal("wrong recovered scope")
	}
	assertOneTerminal(t, f, m.ID)
}
