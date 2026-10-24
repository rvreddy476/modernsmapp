package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/atpost/identity-user-service/internal/service"
	"github.com/atpost/identity-user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// memSettingsRouter serves an in-memory settings row: GET returns it, PUT
// replaces it with what the handler merged. With update nil, the PUT runs the
// real keyword normaliser before storing.
func memSettingsRouter(t *testing.T, row *store.UserSettings, update func(*store.UserSettings) (*store.UserSettings, error)) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := &stubUserService{
		getSettingsFn: func(id uuid.UUID) (*store.UserSettings, error) {
			cp := *row
			cp.DmFilteredKeywords = append([]string{}, row.DmFilteredKeywords...)
			return &cp, nil
		},
		updateFn: update,
	}
	if svc.updateFn == nil {
		svc.updateFn = func(s *store.UserSettings) (*store.UserSettings, error) {
			kw, err := service.NormaliseFilteredKeywords(s.DmFilteredKeywords)
			if err != nil {
				return nil, err
			}
			s.DmFilteredKeywords = kw
			*row = *s
			return s, nil
		}
	}
	r := gin.New()
	New(svc, nil).RegisterRoutes(r, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() })
	return r
}

func defaultRow(id uuid.UUID) *store.UserSettings {
	return &store.UserSettings{
		UserID: id, AccountVisibility: "public", AllowMessagesFrom: "everyone", AllowCommentsFrom: "everyone",
		WhoCanMessage: "everyone_message_requests",
		WhoCanCall:    "connections_only", WhoCanAddToGroups: "connections_only",
		WhoCanSeeOnlineStatus: "connections_only", WhoCanSeeReadReceipts: "connections_only",
		WhoCanSeeLastSeen: "connections_only", WhoCanSeeProfilePhoto: "everyone",
		ChatAvailability: "enabled", SendTypingIndicators: true, ShowMessagePreview: true,
		DmSafeMode: true, DmSensitiveMediaFilter: false, DmFilteredKeywords: []string{}, ChatBubbleStyle: "default",
		WhoCanMention: "everyone", WhoCanTag: "everyone",
	}
}

func do(t *testing.T, r *gin.Engine, method, body string, id uuid.UUID) (int, map[string]json.RawMessage) {
	t.Helper()
	var rdr *bytes.Buffer
	if body != "" {
		rdr = bytes.NewBufferString(body)
	} else {
		rdr = &bytes.Buffer{}
	}
	req := httptest.NewRequest(method, "/v1/users/me/settings", rdr)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", id.String())
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	var env map[string]json.RawMessage
	if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v (%s)", method, err, resp.Body.String())
	}
	return resp.Code, env
}

func TestSettingsChatKeysRoundTrip(t *testing.T) {
	id := uuid.New()
	row := defaultRow(id)
	r := memSettingsRouter(t, row, nil)

	// Defaults are visible on GET, keywords as [] (never null).
	code, env := do(t, r, http.MethodGet, "", id)
	if code != http.StatusOK {
		t.Fatalf("GET status %d", code)
	}
	var got map[string]interface{}
	_ = json.Unmarshal(env["data"], &got)
	if got["dm_safe_mode"] != true || got["dm_sensitive_media_filter"] != false ||
		got["chat_bubble_style"] != "default" || !reflect.DeepEqual(got["dm_filtered_keywords"], []interface{}{}) {
		t.Fatalf("defaults wrong: %v", got)
	}

	code, env = do(t, r, http.MethodPut, `{"dm_safe_mode":false,"dm_sensitive_media_filter":true,
		"dm_filtered_keywords":["  Crypto ","LOAN","crypto"],"chat_bubble_style":"ember"}`, id)
	if code != http.StatusOK {
		t.Fatalf("PUT status %d: %s", code, env["error"])
	}

	code, env = do(t, r, http.MethodGet, "", id)
	got = nil
	_ = json.Unmarshal(env["data"], &got)
	if code != http.StatusOK || got["dm_safe_mode"] != false || got["dm_sensitive_media_filter"] != true ||
		got["chat_bubble_style"] != "ember" ||
		!reflect.DeepEqual(got["dm_filtered_keywords"], []interface{}{"crypto", "loan"}) {
		t.Fatalf("round trip wrong: %v", got)
	}
	// Untouched keys survive a partial PUT.
	if got["who_can_message"] != "everyone_message_requests" || got["show_message_preview"] != true {
		t.Fatalf("partial PUT clobbered other keys: %v", got)
	}

	// A PUT without dm_filtered_keywords keeps the list; [] clears it.
	do(t, r, http.MethodPut, `{"chat_bubble_style":"mono"}`, id)
	if !reflect.DeepEqual(row.DmFilteredKeywords, []string{"crypto", "loan"}) || row.ChatBubbleStyle != "mono" {
		t.Fatalf("absent key changed the list: %+v", row)
	}
	do(t, r, http.MethodPut, `{"dm_filtered_keywords":[]}`, id)
	if len(row.DmFilteredKeywords) != 0 {
		t.Fatalf("[] should clear the list, got %q", row.DmFilteredKeywords)
	}
}

// A field error from the service becomes 400 INVALID_REQUEST with
// details {field, code}.
func TestSettingsFieldErrorDetails(t *testing.T) {
	id := uuid.New()
	row := defaultRow(id)
	r := memSettingsRouter(t, row, func(s *store.UserSettings) (*store.UserSettings, error) {
		return nil, &service.SettingValidationError{Field: "chat_bubble_style", Code: service.SettingCodeInvalidValue, Value: s.ChatBubbleStyle}
	})
	code, env := do(t, r, http.MethodPut, `{"chat_bubble_style":"sunset"}`, id)
	if code != http.StatusBadRequest {
		t.Fatalf("status %d", code)
	}
	var apiErr struct {
		Code    string            `json:"code"`
		Details map[string]string `json:"details"`
	}
	if err := json.Unmarshal(env["error"], &apiErr); err != nil {
		t.Fatal(err)
	}
	if apiErr.Code != "INVALID_REQUEST" || apiErr.Details["field"] != "chat_bubble_style" || apiErr.Details["code"] != "INVALID_VALUE" {
		t.Fatalf("error body wrong: %+v", apiErr)
	}

	// Through the real normaliser: too many keywords is a 400 with its code.
	r = memSettingsRouter(t, defaultRow(id), nil)
	many := make([]string, 101)
	for i := range many {
		many[i] = "w"
	}
	body, _ := json.Marshal(map[string]interface{}{"dm_filtered_keywords": many})
	code, env = do(t, r, http.MethodPut, string(body), id)
	_ = json.Unmarshal(env["error"], &apiErr)
	if code != http.StatusBadRequest || apiErr.Details["code"] != service.SettingCodeTooManyKeywords {
		t.Fatalf("want 400 TOO_MANY_KEYWORDS, got %d %+v", code, apiErr)
	}
}
