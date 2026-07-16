package connectmac

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebTransferRecordAuthorizationAndLifecycle(t *testing.T) {
	app, handler, admin, operator := newWebTransferTestApp(t)

	adminRecord := startWebTransferRecord(t, &app, handler, admin, `{"profile":"shared","direction":"push","local_path":"/admin","remote_path":"~/admin"}`)
	operatorRecord := startWebTransferRecord(t, &app, handler, operator, `{"profile":"shared","direction":"pull","local_path":"/operator","remote_path":"~/operator"}`)

	rec := serveWebTransfer(t, &app, handler, operator, http.MethodGet, "/api/transfer-records", "")
	operatorRecords := decodeWebTransferRecords(t, rec)
	if len(operatorRecords) != 1 || operatorRecords[0].ID != operatorRecord.ID {
		t.Fatalf("operator records = %+v, want only %s", operatorRecords, operatorRecord.ID)
	}
	rec = serveWebTransfer(t, &app, handler, admin, http.MethodGet, "/api/transfer-records", "")
	adminRecords := decodeWebTransferRecords(t, rec)
	if len(adminRecords) != 1 || adminRecords[0].ID != adminRecord.ID {
		t.Fatalf("admin records = %+v, want only %s", adminRecords, adminRecord.ID)
	}

	update := `{"id":"` + operatorRecord.ID + `","local_job_id":"job-1","status":"running","percent":25}`
	rec = serveWebTransfer(t, &app, handler, admin, http.MethodPost, "/api/transfer-record/update", update)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("admin cross-member update status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = serveWebTransfer(t, &app, handler, admin, http.MethodPost, "/api/transfer-record/delete", `{"id":"`+operatorRecord.ID+`"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("admin cross-member delete status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = serveWebTransfer(t, &app, handler, operator, http.MethodPost, "/api/transfer-record/update", update)
	if rec.Code != http.StatusOK {
		t.Fatalf("milestone update status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = serveWebTransfer(t, &app, handler, operator, http.MethodPost, "/api/transfer-record/update",
		`{"id":"`+operatorRecord.ID+`","local_job_id":"job-1","status":"succeeded","percent":100,"elapsed_ms":1250}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("terminal update status=%d body=%s", rec.Code, rec.Body.String())
	}

	injected := `{"profile":"shared","direction":"push","local_path":"/tmp","remote_path":"~/tmp","member_id":"` + admin.ID + `"}`
	rec = serveWebTransfer(t, &app, handler, operator, http.MethodPost, "/api/transfer-record/start", injected)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("member_id injection status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = serveWebTransfer(t, &app, handler, operator, http.MethodPost, "/api/transfer-record/start",
		`{"profile":"shared","direction":"push","local_path":"/tmp","remote_path":"~/tmp"} {"member_id":"`+admin.ID+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("trailing member_id injection status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = serveWebTransfer(t, &app, handler, operator, http.MethodPost, "/api/transfer-record/start",
		`{"profile":"private","direction":"push","local_path":"/tmp","remote_path":"~/tmp"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("profile authorization status=%d body=%s", rec.Code, rec.Body.String())
	}

	entries := readTestLogEntries(t, app.LogManager)
	createdLog := findTransferLogMessage(t, entries, "created")
	if createdLog.MemberEmail != admin.Email || createdLog.TransferID != adminRecord.ID ||
		createdLog.Profile != "shared" || createdLog.Direction != TransferDirectionPush ||
		createdLog.Status != TransferStatusCreated || createdLog.Percent != 0 {
		t.Fatalf("creation log fields = %+v", createdLog)
	}
	milestoneLog := findTransferLogMessage(t, entries, "milestone")
	if milestoneLog.MemberEmail != operator.Email || milestoneLog.TransferID != operatorRecord.ID ||
		milestoneLog.LocalJobID != "job-1" || milestoneLog.Profile != "shared" ||
		milestoneLog.Direction != TransferDirectionPull || milestoneLog.Status != TransferStatusRunning ||
		milestoneLog.Percent != 25 {
		t.Fatalf("milestone log fields = %+v", milestoneLog)
	}
	terminalLog := findTransferLogMessage(t, entries, "terminal")
	if terminalLog.MemberEmail != operator.Email || terminalLog.TransferID != operatorRecord.ID ||
		terminalLog.LocalJobID != "job-1" || terminalLog.Profile != "shared" ||
		terminalLog.Direction != TransferDirectionPull || terminalLog.Status != TransferStatusSucceeded ||
		terminalLog.Percent != 100 || terminalLog.ElapsedMS != 1250 {
		t.Fatalf("terminal log fields = %+v", terminalLog)
	}
	authLog := findTransferLogMessage(t, entries, "authorization rejected: transfer profile access denied")
	if authLog.MemberEmail != operator.Email || authLog.Profile != "private" ||
		authLog.Direction != TransferDirectionPush {
		t.Fatalf("authorization rejection log fields = %+v", authLog)
	}
}

func TestWebTransferRecordUpdateValidationErrorIsBadRequest(t *testing.T) {
	app, handler, _, operator := newWebTransferTestApp(t)
	record := startWebTransferRecord(t, &app, handler, operator, `{"profile":"shared","direction":"push","local_path":"/tmp","remote_path":"~/tmp"}`)
	before := len(readTestLogEntries(t, app.LogManager))
	rec := serveWebTransfer(t, &app, handler, operator, http.MethodPost, "/api/transfer-record/update",
		`{"id":"`+record.ID+`","local_job_id":"job-1","status":"running","percent":101}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	entries := readTestLogEntries(t, app.LogManager)
	for _, entry := range entries[before:] {
		if strings.Contains(entry.Message, "persistence error") {
			t.Fatalf("validation error logged as persistence error: %+v", entry)
		}
	}
}

func TestWebTransferRecordStartValidationErrorIsBadRequest(t *testing.T) {
	app, handler, _, operator := newWebTransferTestApp(t)
	rec := serveWebTransfer(t, &app, handler, operator, http.MethodPost, "/api/transfer-record/start",
		`{"profile":"shared","direction":"sideways","local_path":"/tmp","remote_path":"~/tmp"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	files, err := app.LogManager.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		return
	}
	for _, entry := range readTestLogEntries(t, app.LogManager) {
		if strings.Contains(entry.Message, "persistence error") {
			t.Fatalf("start validation error logged as persistence error: %+v", entry)
		}
	}
}

func TestWebTransferRecordUpdatePersistenceErrorIsInternalServerError(t *testing.T) {
	app, _, _, operator := newWebTransferTestApp(t)
	store := failingTransferRepository{MemberRepository: app.MemberStore, updateErr: errors.New("database unavailable")}
	app.MemberStore = store
	handler := app.newWebHandler("")
	rec := serveWebTransfer(t, &app, handler, operator, http.MethodPost, "/api/transfer-record/update",
		`{"id":"transfer-1","local_job_id":"job-1","status":"running","percent":25}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	entry := findTransferLogMessage(t, readTestLogEntries(t, app.LogManager), "persistence error")
	if entry.MemberEmail != operator.Email || entry.TransferID != "transfer-1" ||
		entry.LocalJobID != "job-1" || entry.Status != TransferStatusRunning || entry.Percent != 25 {
		t.Fatalf("update persistence error log fields = %+v", entry)
	}
}

func TestWebTransferRecordPersistenceErrorIsLogged(t *testing.T) {
	app, _, _, operator := newWebTransferTestApp(t)
	app.MemberStore = failingTransferRepository{MemberRepository: app.MemberStore, createErr: errors.New("database unavailable")}
	handler := app.newWebHandler("")
	rec := serveWebTransfer(t, &app, handler, operator, http.MethodPost, "/api/transfer-record/start",
		`{"profile":"shared","direction":"push","local_path":"/tmp","remote_path":"~/tmp"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	entry := findTransferLogMessage(t, readTestLogEntries(t, app.LogManager), "persistence error")
	if entry.MemberEmail != operator.Email || entry.Profile != "shared" ||
		entry.Direction != TransferDirectionPush || entry.Status != TransferStatusCreated {
		t.Fatalf("persistence error log fields = %+v", entry)
	}
}

func newWebTransferTestApp(t *testing.T) (App, http.Handler, Member, Member) {
	t.Helper()
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	admin, err := app.MemberStore.SetupAdmin("Admin", "admin@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	operator, err := app.MemberStore.AddMember("Operator", "operator@example.com", "operator")
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"shared", "private"} {
		_, err = app.MemberStore.UpsertManagedProfile(Profile{
			Name: profile,
			AWS:  AWSConfig{AccountEmail: profile + "@example.com", Region: "us-west-2"},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.MemberStore.AssignProfileAccess("shared", operator.Email); err != nil {
		t.Fatal(err)
	}
	return app, app.newWebHandler(""), admin, operator
}

func startWebTransferRecord(t *testing.T, app *App, handler http.Handler, member Member, body string) TransferRecord {
	t.Helper()
	rec := serveWebTransfer(t, app, handler, member, http.MethodPost, "/api/transfer-record/start", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("start status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Data struct {
			Record TransferRecord `json:"record"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Data.Record
}

func serveWebTransfer(t *testing.T, app *App, handler http.Handler, member Member, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	session := httptest.NewRecorder()
	if err := app.setWebSession(session, member); err != nil {
		t.Fatal(err)
	}
	req.AddCookie(session.Result().Cookies()[0])
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func findTransferLogMessage(t *testing.T, entries []LogEntry, want string) LogEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Action == webTransferLogAction && strings.Contains(entry.Message, want) {
			return entry
		}
	}
	t.Fatalf("missing transfer log %q in %+v", want, entries)
	return LogEntry{}
}

func decodeWebTransferRecords(t *testing.T, rec *httptest.ResponseRecorder) []TransferRecord {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Data struct {
			Records []TransferRecord `json:"records"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Data.Records
}

type failingTransferRepository struct {
	MemberRepository
	createErr error
	updateErr error
}

func (r failingTransferRepository) CreateTransferRecord(string, TransferRecord) (TransferRecord, error) {
	return TransferRecord{}, r.createErr
}

func (r failingTransferRepository) UpdateTransferRecord(string, string, string, func(TransferRecord) (TransferRecord, error)) (TransferRecord, error) {
	return TransferRecord{}, r.updateErr
}
