package facade_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/facade"
)

func TestContract_FacadeMissingMethodValidation(t *testing.T) {
	client := newClient(t, httptest.NewServer(http.NotFoundHandler()).URL)
	if _, err := client.GetTask(context.Background(), ""); err == nil {
		t.Fatal("GetTask should reject empty task id")
	}
}

func TestContract_FacadeGetTaskAndNotifications(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		switch r.URL.Path {
		case "/api/v2/tasks/task-1":
			writeJSON(t, w, http.StatusOK, map[string]interface{}{
				"data":       map[string]interface{}{"task_id": "task-1", "project_id": "p1", "title": "Build", "status": "queued", "resource_version": 2},
				"request_id": "req-task",
			})
		case "/api/v2/notifications":
			if r.URL.Query().Get("unread_only") != "true" || r.URL.Query().Get("limit") != "3" || r.URL.Query().Get("cursor") != "next" {
				t.Errorf("unexpected query: %s", r.URL.RawQuery)
			}
			writeJSON(t, w, http.StatusOK, map[string]interface{}{
				"data":       []map[string]interface{}{{"notification_id": "n1", "type": "task", "severity": "info", "source": "acc", "title": "Ready"}},
				"page":       map[string]interface{}{"limit": 3, "next_cursor": "after"},
				"request_id": "req-notify",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := newClient(t, srv.URL)
	task, err := client.GetTask(context.Background(), "task-1")
	if err != nil || task.Data.TaskID != "task-1" || task.Data.Status != "queued" {
		t.Fatalf("GetTask = %+v, err=%v", task, err)
	}
	notifications, err := client.ListNotifications(context.Background(), facade.ListNotificationsQuery{UnreadOnly: true, Limit: 3, Cursor: "next"})
	if err != nil || len(notifications.Data) != 1 || !notifications.Page.HasMore() || notifications.Page.NextCursor != "after" {
		t.Fatalf("ListNotifications = %+v, err=%v", notifications, err)
	}
}

func TestContract_FacadeAPIErrorEnvelopeIncludesCorrelation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusConflict, map[string]interface{}{
			"error":      map[string]interface{}{"code": "version_conflict", "message": "stale", "retryable": true},
			"request_id": "req-err", "correlation_id": "corr-err",
		})
	}))
	defer srv.Close()

	_, err := newClient(t, srv.URL).GetTask(context.Background(), "task-1")
	var apiErr *facade.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *facade.APIError", err)
	}
	if apiErr.Status != http.StatusConflict || apiErr.Code != "version_conflict" || !apiErr.Retryable || apiErr.RequestID != "req-err" || apiErr.CorrelationID != "corr-err" {
		t.Fatalf("unexpected API error: %+v", apiErr)
	}
}
